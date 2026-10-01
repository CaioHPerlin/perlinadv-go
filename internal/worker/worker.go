package worker

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/caiohperlin/perlinadv/internal/calendar"
	"github.com/caiohperlin/perlinadv/internal/extract"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type Worker struct {
	DB      *sql.DB
	HTTP    *http.Client
	BaseURL string
	Mode    string
	LLM     extract.Provider
}

var ErrNoEdition = errors.New("DJEN edition unavailable")
var ErrRateLimited = errors.New("DJEN rate limited")

func New() (Worker, error) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		return Worker{}, errors.New("DATABASE_URL required")
	}
	db, e := sql.Open("pgx", dsn)
	if e != nil {
		return Worker{}, e
	}
	db.SetMaxOpenConns(10)
	if e = db.Ping(); e != nil {
		return Worker{}, e
	}
	base := os.Getenv("DJEN_BASE_URL")
	if base == "" {
		base = "https://comunicaapi.pje.jus.br/api/v1"
	}
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("DJEN_COLLECTION_MODE")))
	if mode == "" {
		mode = "oab"
	}
	if mode != "oab" && mode != "caderno" {
		db.Close()
		return Worker{}, errors.New("DJEN_COLLECTION_MODE must be oab or caderno")
	}
	return Worker{DB: db, HTTP: &http.Client{Timeout: 60 * time.Second}, BaseURL: strings.TrimRight(base, "/"), Mode: mode, LLM: extract.CloudflareFromEnv()}, nil
}
func (w Worker) Close() { _ = w.DB.Close() }
func (w Worker) Replay(ctx context.Context, tribunal, date string) error {
	if tribunal != "TJMS" && tribunal != "TRF3" && tribunal != "TRT24" {
		return errors.New("unsupported tribunal")
	}
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return err
	}
	return w.ingestEdition(ctx, tribunal, date)
}
func (w Worker) RunOnce(ctx context.Context) error {
	locked := false
	if e := w.DB.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(7242026)`).Scan(&locked); e != nil {
		return e
	}
	if !locked {
		return nil
	}
	defer w.DB.ExecContext(context.Background(), `SELECT pg_advisory_unlock(7242026)`)
	zone, _ := time.LoadLocation("America/Campo_Grande")
	today := time.Now().In(zone)
	var collectionErr error
	if w.Mode != "caderno" {
		collectionErr = w.pollLawyers(ctx, today)
		if collectionErr != nil {
			log.Printf("DJEN OAB poll: %v", collectionErr)
		}
	} else {
		tribunals := []string{"TJMS", "TRF3"}
		var labor bool
		_ = w.DB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE active AND labor_practice AND role='LAWYER')`).Scan(&labor)
		if labor {
			tribunals = append(tribunals, "TRT24")
		}
		rateLimited := false
		for _, tribunal := range tribunals {
			for _, date := range previousBusinessDays(today, 7) {
				if err := w.ingestEdition(ctx, tribunal, date); err != nil {
					if errors.Is(err, ErrNoEdition) {
						continue
					}
					log.Printf("DJEN %s %s: %v", tribunal, date, err)
					_, _ = w.DB.ExecContext(ctx, `INSERT INTO djen_editions(tribunal,edition_on,source_version,status,error) VALUES($1,$2,'error','FAILED',$3) ON CONFLICT(tribunal,edition_on,medium,source_version) DO UPDATE SET status='FAILED',error=excluded.error`, tribunal, date, err.Error())
					if errors.Is(err, ErrRateLimited) {
						rateLimited = true
						break
					}
				}
			}
			if rateLimited {
				break
			}
		}
	}
	_, _ = w.DB.ExecContext(ctx, `DELETE FROM recent_publication_matches WHERE expires_at<now()`)
	_, _ = w.DB.ExecContext(ctx, `DELETE FROM publications p WHERE NOT EXISTS(SELECT 1 FROM user_publications up WHERE up.publication_id=p.id) AND NOT EXISTS(SELECT 1 FROM recent_publication_matches m WHERE m.source_id=p.source_id)`)
	return collectionErr
}
func previousBusinessDays(today time.Time, n int) []string {
	days := []string{}
	for d := today.AddDate(0, 0, -1); len(days) < n; d = d.AddDate(0, 0, -1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			days = append(days, d.Format("2006-01-02"))
		}
	}
	return days
}

type manifest struct{ URL, Hash, Version string }

func pick(m any, keys ...string) string {
	obj, ok := m.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range keys {
		for name, value := range obj {
			if strings.EqualFold(name, key) {
				if s, ok := value.(string); ok {
					return s
				}
				if n, ok := value.(float64); ok {
					return strconv.FormatInt(int64(n), 10)
				}
			}
		}
	}
	for _, value := range obj {
		if child, ok := value.(map[string]any); ok {
			if s := pick(child, keys...); s != "" {
				return s
			}
		}
		if list, ok := value.([]any); ok {
			for _, child := range list {
				if s := pick(child, keys...); s != "" {
					return s
				}
			}
		}
	}
	return ""
}
func (w Worker) fetchManifest(ctx context.Context, tribunal, date string) (manifest, error) {
	endpoint := fmt.Sprintf("%s/caderno/%s/%s/D", w.BaseURL, tribunal, date)
	resp, e := w.get(ctx, endpoint)
	if e != nil {
		return manifest{}, e
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return manifest{}, ErrNoEdition
	}
	if resp.StatusCode == 429 {
		return manifest{}, ErrRateLimited
	}
	if resp.StatusCode != 200 {
		return manifest{}, fmt.Errorf("manifest HTTP %d", resp.StatusCode)
	}
	var obj any
	if e = json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&obj); e != nil {
		return manifest{}, e
	}
	m := manifest{URL: pick(obj, "url", "downloadUrl", "urlArquivo", "link"), Hash: pick(obj, "hash", "sha256"), Version: pick(obj, "versao", "version")}
	if m.URL == "" {
		return m, errors.New("manifest has no download URL")
	}
	u, e := url.Parse(m.URL)
	if e != nil || u.Scheme != "https" {
		return m, errors.New("invalid signed download URL")
	}
	return m, nil
}
func (w Worker) ingestEdition(ctx context.Context, tribunal, date string) error {
	m, e := w.fetchManifest(ctx, tribunal, date)
	if e != nil {
		return e
	}
	var current string
	_ = w.DB.QueryRowContext(ctx, `SELECT source_hash FROM djen_editions WHERE tribunal=$1 AND edition_on=$2 AND medium='D' AND source_version=$3 AND status='DONE'`, tribunal, date, m.Version).Scan(&current)
	if current == m.Hash && current != "" {
		return nil
	}
	file, e := os.CreateTemp("", "perlinadv-djen-*.zip")
	if e != nil {
		return e
	}
	defer os.Remove(file.Name())
	defer file.Close()
	resp, e := w.get(ctx, m.URL)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("ZIP HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	n, e := io.Copy(io.MultiWriter(file, h), io.LimitReader(resp.Body, 300<<20))
	if e != nil {
		return e
	}
	if n >= 300<<20 {
		return errors.New("ZIP too large")
	}
	if len(m.Hash) == 64 && hex.EncodeToString(h.Sum(nil)) != strings.ToLower(m.Hash) {
		return errors.New("ZIP checksum mismatch")
	}
	st, e := file.Stat()
	if e != nil {
		return e
	}
	z, e := zip.NewReader(file, st.Size())
	if e != nil {
		return e
	}
	count := 0
	for _, entry := range z.File {
		if entry.FileInfo().IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name), ".json") {
			continue
		}
		if entry.UncompressedSize64 > 1<<30 {
			return errors.New("JSON entry too large")
		}
		reader, e := entry.Open()
		if e != nil {
			return e
		}
		e = consumeJSON(reader, func(item djenItem) error { count++; return w.saveItem(ctx, item, tribunal, m.Version) })
		reader.Close()
		if e != nil {
			return e
		}
	}
	_, e = w.DB.ExecContext(ctx, `INSERT INTO djen_editions(tribunal,edition_on,source_version,source_hash,status,item_count,processed_at) VALUES($1,$2,$3,$4,'DONE',$5,now()) ON CONFLICT(tribunal,edition_on,medium,source_version) DO UPDATE SET source_hash=excluded.source_hash,status='DONE',item_count=excluded.item_count,processed_at=now(),error=''`, tribunal, date, m.Version, m.Hash, count)
	_, _ = w.DB.ExecContext(ctx, `DELETE FROM djen_editions WHERE tribunal=$1 AND edition_on=$2 AND source_version='error'`, tribunal, date)
	return e
}
func (w Worker) get(ctx context.Context, target string) (*http.Response, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, "GET", target, nil)
		if err != nil {
			return nil, err
		}
		resp, err := w.HTTP.Do(req)
		if err == nil {
			if resp.StatusCode != 429 && resp.StatusCode < 500 {
				return resp, nil
			}
			if resp.StatusCode == 429 {
				last = ErrRateLimited
			} else {
				last = fmt.Errorf("DJEN HTTP %d", resp.StatusCode)
			}
			resp.Body.Close()
		} else {
			last = err
		}
		delay := time.Duration(1<<attempt) * time.Second
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return nil, last
}

type advocate struct {
	Number string `json:"numero_oab"`
	UF     string `json:"uf_oab"`
}
type recipientAdvocate struct {
	Advocate advocate `json:"advogado"`
	Number   string   `json:"numero_oab"`
	UF       string   `json:"uf_oab"`
}
type djenItem struct {
	ID           json.Number         `json:"id"`
	Hash         string              `json:"hash"`
	Text         string              `json:"texto"`
	Available    string              `json:"data_disponibilizacao"`
	AvailableAlt string              `json:"datadisponibilizacao"`
	Tribunal     string              `json:"siglaTribunal"`
	Kind         string              `json:"tipoComunicacao"`
	Status       string              `json:"status"`
	Unit         string              `json:"nomeOrgao"`
	CaseNumber   string              `json:"numero_processo"`
	Link         string              `json:"link"`
	Active       *bool               `json:"ativo"`
	Advocates    []recipientAdvocate `json:"destinatarioadvogados"`
}

func consumeJSON(r io.Reader, fn func(djenItem) error) error {
	d := json.NewDecoder(r)
	d.UseNumber()
	for {
		first, err := d.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		switch first {
		case json.Delim('['):
			for d.More() {
				var item djenItem
				if err = d.Decode(&item); err != nil {
					return err
				}
				if err = fn(item); err != nil {
					return err
				}
			}
			if _, err = d.Token(); err != nil {
				return err
			}
		case json.Delim('{'):
			fields := map[string]json.RawMessage{}
			wrapper := false
			for d.More() {
				keyToken, err := d.Token()
				if err != nil {
					return err
				}
				key := keyToken.(string)
				if key == "items" || key == "comunicacoes" {
					wrapper = true
					start, err := d.Token()
					if err != nil {
						return err
					}
					if start != json.Delim('[') {
						return errors.New("items is not an array")
					}
					for d.More() {
						var item djenItem
						if err = d.Decode(&item); err != nil {
							return err
						}
						if err = fn(item); err != nil {
							return err
						}
					}
					if _, err = d.Token(); err != nil {
						return err
					}
				} else {
					var raw json.RawMessage
					if err = d.Decode(&raw); err != nil {
						return err
					}
					fields[key] = raw
				}
			}
			if _, err = d.Token(); err != nil {
				return err
			}
			if !wrapper {
				raw, _ := json.Marshal(fields)
				var item djenItem
				if err = json.Unmarshal(raw, &item); err != nil {
					return err
				}
				if item.ID != "" {
					if err = fn(item); err != nil {
						return err
					}
				}
			}
		default:
			return errors.New("unsupported JSON shape")
		}
	}
}

var tags = regexp.MustCompile(`<[^>]+>`)
var whitespace = regexp.MustCompile(`\s+`)

func textOnly(s string) string {
	return strings.TrimSpace(whitespace.ReplaceAllString(html.UnescapeString(tags.ReplaceAllString(s, " ")), " "))
}
func dateOnly(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}
func normalizeNumber(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
}
func normalizeOAB(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' {
			return r
		}
		return -1
	}, strings.ToUpper(s))
}
func (w Worker) saveItem(ctx context.Context, item djenItem, tribunal, version string) error {
	source := item.ID.String()
	if source == "" || source == "0" {
		return nil
	}
	available := dateOnly(item.Available)
	if available == "" {
		available = dateOnly(item.AvailableAlt)
	}
	if _, e := time.Parse("2006-01-02", available); e != nil {
		return nil
	}
	if item.Active != nil && !*item.Active {
		return nil
	}
	caseNo := normalizeNumber(item.CaseNumber)
	plain := textOnly(item.Text)
	title := item.Kind
	if title == "" {
		title = "Comunicação"
	}
	var id, oldHash string
	_ = w.DB.QueryRowContext(ctx, `SELECT id::text,source_hash FROM publications WHERE source_id=$1`, "DJEN:"+source).Scan(&id, &oldHash)
	e := w.DB.QueryRowContext(ctx, `INSERT INTO publications(source_id,source_hash,tribunal,judicial_unit,case_number,title,content_html,content_text,available_on,situation,source_url,source_version) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(source_id) DO UPDATE SET source_hash=excluded.source_hash,tribunal=excluded.tribunal,judicial_unit=excluded.judicial_unit,case_number=excluded.case_number,title=excluded.title,content_html=excluded.content_html,content_text=excluded.content_text,available_on=excluded.available_on,situation=excluded.situation,source_url=excluded.source_url,source_version=excluded.source_version,updated_at=now() RETURNING id::text`, "DJEN:"+source, item.Hash, tribunal, item.Unit, caseNo, title, item.Text, plain, available, item.Status, item.Link, version).Scan(&id)
	if e != nil {
		return e
	}
	if oldHash != "" && oldHash != item.Hash {
		_, _ = w.DB.ExecContext(ctx, `UPDATE user_publications SET analysis_status='PENDING',analysis_attempts=0,llm_assisted=FALSE,analysis_started_at=NULL,analysis_next_attempt_at=NULL WHERE publication_id=$1`, id)
		_, _ = w.DB.ExecContext(ctx, `UPDATE deadline_suggestions SET status='NEEDS_REVIEW' WHERE publication_id=$1 AND status='PENDING'`, id)
		_, _ = w.DB.ExecContext(ctx, `UPDATE deadlines SET needs_review=TRUE WHERE publication_id=$1`, id)
	}
	for _, a := range item.Advocates {
		number, uf := a.Advocate.Number, a.Advocate.UF
		if number == "" {
			number = a.Number
			uf = a.UF
		}
		number = normalizeOAB(number)
		uf = strings.ToUpper(strings.TrimSpace(uf))
		if number == "" || len(uf) != 2 {
			continue
		}
		if day, _ := time.Parse("2006-01-02", available); time.Since(day) < 4*24*time.Hour {
			_, _ = w.DB.ExecContext(ctx, `INSERT INTO recent_publication_matches(source_id,oab_uf,oab_number,available_on,expires_at) VALUES($1,$2,$3,$4,$4::date+interval '4 days') ON CONFLICT(source_id,oab_uf,oab_number) DO UPDATE SET expires_at=excluded.expires_at`, "DJEN:"+source, uf, number, available)
		}
		_, _ = w.DB.ExecContext(ctx, `INSERT INTO user_publications(user_id,publication_id) SELECT id,$1 FROM users WHERE oab_number=$2 AND oab_uf=$3 AND active AND llm_consent AND (labor_practice OR $4<>'TRT24') AND $5::date >= (monitoring_started_at AT TIME ZONE 'America/Campo_Grande')::date-1 ON CONFLICT DO NOTHING`, id, number, uf, tribunal, available)
	}
	_, _ = w.DB.ExecContext(ctx, `INSERT INTO case_publications(user_id,case_id,publication_id) SELECT c.user_id,c.id,$1 FROM cases c JOIN user_publications up ON up.user_id=c.user_id AND up.publication_id=$1 WHERE c.case_number=$2 ON CONFLICT DO NOTHING`, id, caseNo)
	return nil
}
func (w Worker) analyze(ctx context.Context) error {
	_, _ = w.DB.ExecContext(ctx, `UPDATE user_publications SET analysis_status='FAILED',analysis_started_at=NULL WHERE analysis_status='RUNNING' AND analysis_started_at<now()-interval '2 minutes' AND analysis_attempts>=3`)
	rows, err := w.DB.QueryContext(ctx, `WITH candidate AS (
		SELECT user_id,publication_id,analysis_status AS previous_status FROM user_publications
		WHERE analysis_requested_at IS NOT NULL AND EXISTS(SELECT 1 FROM users WHERE users.id=user_publications.user_id AND users.llm_consent) AND (
			(analysis_status IN ('PENDING','RETRY') AND coalesce(analysis_next_attempt_at,'-infinity'::timestamptz)<=now() AND analysis_attempts<3)
			OR (analysis_status='RUNNING' AND analysis_started_at<now()-interval '2 minutes' AND analysis_attempts<3)
		)
		ORDER BY coalesce(analysis_next_attempt_at,created_at),created_at
		LIMIT 100 FOR UPDATE SKIP LOCKED
	), claimed AS (
		UPDATE user_publications up SET analysis_status='RUNNING',analysis_started_at=now(),analysis_next_attempt_at=NULL,
			analysis_attempts=up.analysis_attempts+CASE WHEN c.previous_status='RUNNING' THEN 1 ELSE 0 END
		FROM candidate c WHERE up.user_id=c.user_id AND up.publication_id=c.publication_id
		RETURNING up.user_id,up.publication_id
	)
	SELECT up.user_id::text,p.id::text,p.content_text,p.available_on::text,p.tribunal,u.llm_consent,coalesce(c.locality,''),coalesce(c.id::text,'')
	FROM claimed cl JOIN user_publications up ON up.user_id=cl.user_id AND up.publication_id=cl.publication_id
	JOIN publications p ON p.id=up.publication_id JOIN users u ON u.id=up.user_id
	LEFT JOIN LATERAL (SELECT id,locality FROM cases WHERE user_id=up.user_id AND case_number=p.case_number AND tribunal=p.tribunal LIMIT 1) c ON true
	ORDER BY up.created_at`)
	if err != nil {
		return err
	}
	tasks := []analysisTask{}
	for rows.Next() {
		var task analysisTask
		if err := rows.Scan(&task.user, &task.pub, &task.text, &task.available, &task.tribunal, &task.consent, &task.locality, &task.caseID); err != nil {
			rows.Close()
			return err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, task := range tasks {
		if err := w.analyzeTask(ctx, task); err != nil {
			log.Printf("analysis of publication %s: %v", task.pub, err)
			_, _ = w.DB.ExecContext(context.Background(), `UPDATE user_publications SET analysis_status=CASE WHEN analysis_attempts>=2 THEN 'FAILED' ELSE 'RETRY' END,analysis_attempts=analysis_attempts+1,analysis_started_at=NULL,analysis_next_attempt_at=CASE WHEN analysis_attempts<2 THEN now()+make_interval(mins=>power(2,analysis_attempts)::int) ELSE NULL END WHERE user_id=$1 AND publication_id=$2 AND analysis_status='RUNNING'`, task.user, task.pub)
		}
	}
	return nil
}

type analysisTask struct {
	user, pub, text, available, tribunal, locality, caseID string
	consent                                                bool
}

func (w Worker) analyzeTask(ctx context.Context, t analysisTask) error {
	candidates := extract.Rules(t.text)
	status := "DONE"
	llmAssisted := false
	if len(candidates) == 0 && t.consent {
		llmAssisted = true
		if _, err := w.DB.ExecContext(ctx, `UPDATE user_publications SET llm_assisted=TRUE WHERE user_id=$1 AND publication_id=$2`, t.user, t.pub); err != nil {
			return err
		}
		var err error
		provider := w.LLM
		if provider == nil {
			provider = extract.CloudflareFromEnv()
		}
		candidates, err = provider.Extract(ctx, t.text)
		if err != nil {
			status = "RETRY"
		}
	}
	for _, c := range candidates {
		var due, pub, first string
		audit := []byte(`{"reason":"calendar coverage or case locality incomplete"}`)
		if t.locality != "" && c.Unit == "BUSINESS_DAYS" {
			result, err := calendar.Calculate(ctx, calendar.DatabaseChecker{DB: w.DB}, t.available, t.tribunal, t.locality, c.DurationDays)
			if err == nil {
				due = result.DueOn
				pub = result.PublicationOn
				first = result.FirstCountedOn
				for _, versionID := range result.VersionIDs {
					var title, url string
					if w.DB.QueryRowContext(ctx, `SELECT source_title,source_url FROM court_calendar_versions WHERE id=$1`, versionID).Scan(&title, &url) == nil {
						result.Authorities = append(result.Authorities, calendar.Authority{VersionID: versionID, Title: title, URL: url})
					}
				}
				audit = calendar.JSON(result)
			}
		}
		_, err := w.DB.ExecContext(ctx, `INSERT INTO deadline_suggestions(user_id,publication_id,action,evidence,duration_days,unit,method,rule_version,suggested_due_on,publication_on,first_counted_on,calculation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT(user_id,publication_id,method,rule_version,evidence) DO UPDATE SET suggested_due_on=excluded.suggested_due_on,publication_on=excluded.publication_on,first_counted_on=excluded.first_counted_on,calculation=excluded.calculation,updated_at=now()`, t.user, t.pub, c.Action, c.Evidence, c.DurationDays, c.Unit, c.Method, c.RuleVersion, nullIfEmpty(due), nullIfEmpty(pub), nullIfEmpty(first), audit)
		if err != nil {
			return err
		}
	}
	_, err := w.DB.ExecContext(ctx, `UPDATE user_publications SET analysis_status=CASE WHEN $3='RETRY' AND analysis_attempts>=2 THEN 'FAILED' ELSE $3 END,analysis_attempts=analysis_attempts+1,llm_assisted=llm_assisted OR $4,analysis_started_at=NULL,analysis_next_attempt_at=CASE WHEN $3='RETRY' AND analysis_attempts<2 THEN now()+make_interval(mins=>power(2,analysis_attempts)::int) ELSE NULL END WHERE user_id=$1 AND publication_id=$2`, t.user, t.pub, status, llmAssisted)
	return err
}
func (w Worker) ProcessAnalysis(ctx context.Context) error { return w.analyze(ctx) }

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
