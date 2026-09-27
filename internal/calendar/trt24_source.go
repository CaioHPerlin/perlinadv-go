package calendar

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const TRT24CalendarURL = "https://www.trt24.jus.br/calendario"

type ImportDay struct {
	Day          string `json:"day"`
	Effect       string `json:"effect"`
	Title        string `json:"title"`
	AuthorityURL string `json:"authorityUrl"`
}
type ImportDraft struct {
	Tribunal      string      `json:"tribunal"`
	ScopeType     string      `json:"scopeType"`
	ScopeKey      string      `json:"scopeKey"`
	CoverageStart string      `json:"coverageStart"`
	CoverageEnd   string      `json:"coverageEnd"`
	SourceTitle   string      `json:"sourceTitle"`
	SourceURL     string      `json:"sourceUrl"`
	Days          []ImportDay `json:"days"`
}

// PrepareTRT24 reads the official holiday table into drafts. An administrator must
// compare each draft with the published act and later amendments before approval.
func PrepareTRT24(r io.Reader, year int) ([]ImportDraft, error) {
	if year < 2000 || year > 2100 {
		return nil, errors.New("invalid calendar year")
	}
	root, err := html.Parse(r)
	if err != nil {
		return nil, err
	}
	rows := [][]string{}
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "tr" {
			cells := []string{}
			for cell := n.FirstChild; cell != nil; cell = cell.NextSibling {
				if cell.Type == html.ElementNode && (cell.Data == "td" || cell.Data == "th") {
					cells = append(cells, strings.Join(strings.Fields(nodeText(cell)), " "))
				}
			}
			if len(cells) == 4 {
				rows = append(rows, cells)
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	if len(rows) < 2 || !strings.EqualFold(rows[0][0], "Data") || !strings.EqualFold(rows[0][1], "Localidade") {
		return nil, errors.New("TRT24 holiday table not found or changed")
	}
	drafts := map[string]*ImportDraft{}
	for _, row := range rows[1:] {
		day, err := time.Parse("02/01/2006", row[0])
		if err != nil {
			return nil, fmt.Errorf("unrecognized TRT24 date %q", row[0])
		}
		if day.Year() != year {
			continue
		}
		locality := strings.ToUpper(strings.TrimSpace(row[1]))
		if locality == "" {
			return nil, errors.New("TRT24 row without locality")
		}
		scope, key := "COMARCA", locality
		if locality == "NACIONAL" || locality == "TRIBUNAL" {
			scope, key = "TRIBUNAL", "TRT24"
		}
		id := scope + ":" + key
		draft := drafts[id]
		if draft == nil {
			draft = &ImportDraft{Tribunal: "TRT24", ScopeType: scope, ScopeKey: key,
				CoverageStart: fmt.Sprintf("%04d-01-01", year), CoverageEnd: fmt.Sprintf("%04d-12-31", year),
				SourceTitle: fmt.Sprintf("TRT24 calendário de feriados %d — conferir atos e alterações", year),
				SourceURL:   TRT24CalendarURL, Days: []ImportDay{}}
			drafts[id] = draft
		}
		text := strings.ToLower(row[2])
		effect := "CLOSED"
		if strings.Contains(text, "suspensão") && (strings.Contains(text, "até as") || strings.Contains(text, "após as")) {
			effect = "PARTIAL"
		} else if strings.Contains(text, "suspensão") && strings.Contains(text, "prazo") {
			effect = "DEADLINE_SUSPENDED"
		}
		draft.Days = append(draft.Days, ImportDay{Day: day.Format("2006-01-02"), Effect: effect,
			Title: strings.TrimSpace(row[2] + " — " + row[3]), AuthorityURL: TRT24CalendarURL})
	}
	if len(drafts) == 0 {
		return nil, errors.New("TRT24 table has no rows for requested year")
	}
	out := make([]ImportDraft, 0, len(drafts))
	for _, d := range drafts {
		out = append(out, *d)
	}
	return out, nil
}
func nodeText(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(nodeText(c))
		b.WriteByte(' ')
	}
	return b.String()
}
