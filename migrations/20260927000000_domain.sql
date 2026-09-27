-- +goose Up
ALTER TABLE users ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN oab_number TEXT;
ALTER TABLE users ADD COLUMN oab_uf CHAR(2);
ALTER TABLE users ADD COLUMN role TEXT NOT NULL DEFAULT 'LAWYER' CHECK (role IN ('LAWYER','ADMIN'));
ALTER TABLE users ADD COLUMN active BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE users ADD COLUMN labor_practice BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN llm_consent BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE users ADD COLUMN monitoring_started_at TIMESTAMPTZ NOT NULL DEFAULT now();
CREATE UNIQUE INDEX users_oab_unique ON users (oab_uf, oab_number) WHERE oab_number IS NOT NULL;

CREATE TABLE password_resets (
    id UUID PRIMARY KEY DEFAULT uuidv7(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash BYTEA NOT NULL UNIQUE, expires_at TIMESTAMPTZ NOT NULL, used_at TIMESTAMPTZ
);

CREATE TABLE cases (
    id UUID PRIMARY KEY DEFAULT uuidv7(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    case_number TEXT NOT NULL, folder_number TEXT NOT NULL DEFAULT '', title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'IN_PROGRESS'
        CHECK (status IN ('IN_PROGRESS','SUSPENDED','ARCHIVED','CLOSED')),
    notes TEXT NOT NULL DEFAULT '', tribunal TEXT NOT NULL DEFAULT '',
    locality TEXT NOT NULL DEFAULT '', judicial_unit TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, case_number)
);
CREATE INDEX cases_owner_idx ON cases(user_id, created_at DESC);

CREATE TABLE publications (
    id UUID PRIMARY KEY DEFAULT uuidv7(), source_id TEXT NOT NULL UNIQUE, source_hash TEXT NOT NULL,
    tribunal TEXT NOT NULL, judicial_unit TEXT NOT NULL DEFAULT '', case_number TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '', content_html TEXT NOT NULL DEFAULT '', content_text TEXT NOT NULL,
    available_on DATE NOT NULL, situation TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '', source_version TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX publications_available_idx ON publications(available_on DESC);
CREATE TABLE user_publications (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    is_read BOOLEAN NOT NULL DEFAULT FALSE, analysis_status TEXT NOT NULL DEFAULT 'PENDING',
    analysis_attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY(user_id, publication_id)
);
CREATE TABLE case_publications (
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    case_id UUID NOT NULL REFERENCES cases(id) ON DELETE CASCADE,
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    manual BOOLEAN NOT NULL DEFAULT FALSE, PRIMARY KEY(user_id, case_id, publication_id)
);

CREATE TABLE court_calendar_versions (
    id UUID PRIMARY KEY DEFAULT uuidv7(), tribunal TEXT NOT NULL,
    scope_type TEXT NOT NULL CHECK(scope_type IN ('TRIBUNAL','SECTION','COMARCA')),
    scope_key TEXT NOT NULL, coverage_start DATE NOT NULL, coverage_end DATE NOT NULL,
    source_title TEXT NOT NULL, source_url TEXT NOT NULL, source_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'DRAFT' CHECK(status IN ('DRAFT','ACTIVE','SUPERSEDED')),
    imported_at TIMESTAMPTZ NOT NULL DEFAULT now(), approved_at TIMESTAMPTZ,
    CHECK (coverage_end >= coverage_start)
);
CREATE TABLE court_calendar_days (
    version_id UUID NOT NULL REFERENCES court_calendar_versions(id) ON DELETE CASCADE,
    day DATE NOT NULL, effect TEXT NOT NULL CHECK(effect IN ('CLOSED','DEADLINE_SUSPENDED','PARTIAL','INFO')),
    title TEXT NOT NULL DEFAULT '', authority_url TEXT NOT NULL DEFAULT '',
    PRIMARY KEY(version_id, day, effect)
);

CREATE TABLE deadline_suggestions (
    id UUID PRIMARY KEY DEFAULT uuidv7(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    publication_id UUID NOT NULL REFERENCES publications(id) ON DELETE CASCADE,
    action TEXT NOT NULL DEFAULT '', evidence TEXT NOT NULL, duration_days INTEGER,
    unit TEXT NOT NULL DEFAULT '', method TEXT NOT NULL, rule_version TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','CONFIRMED','DISCARDED','NEEDS_REVIEW')),
    suggested_due_on DATE, publication_on DATE, first_counted_on DATE,
    calculation JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(user_id, publication_id, method, rule_version, evidence)
);
CREATE INDEX suggestions_owner_status_idx ON deadline_suggestions(user_id,status);

CREATE TABLE deadlines (
    id UUID PRIMARY KEY DEFAULT uuidv7(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    case_id UUID REFERENCES cases(id) ON DELETE SET NULL,
    publication_id UUID REFERENCES publications(id) ON DELETE SET NULL,
    suggestion_id UUID UNIQUE REFERENCES deadline_suggestions(id) ON DELETE SET NULL,
    title TEXT NOT NULL, description TEXT NOT NULL DEFAULT '', due_on DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'ACTIVE' CHECK(status IN ('ACTIVE','COMPLETED','CANCELED')),
    needs_review BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX deadlines_owner_due_idx ON deadlines(user_id,due_on);

CREATE TABLE google_connections (
    user_id UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_cipher BYTEA NOT NULL, calendar_id TEXT NOT NULL DEFAULT 'primary',
    connected_at TIMESTAMPTZ NOT NULL DEFAULT now(), disabled_at TIMESTAMPTZ
);
CREATE TABLE google_oauth_states (
    state_hash BYTEA PRIMARY KEY, user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE google_events (
    deadline_id UUID PRIMARY KEY REFERENCES deadlines(id) ON DELETE CASCADE,
    calendar_id TEXT NOT NULL, event_id TEXT NOT NULL, synced_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE google_jobs (
    id UUID PRIMARY KEY DEFAULT uuidv7(), user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    deadline_id UUID REFERENCES deadlines(id) ON DELETE SET NULL,
    operation TEXT NOT NULL CHECK(operation IN ('UPSERT','DELETE')),
    event_id TEXT NOT NULL DEFAULT '', calendar_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','DONE','FAILED')),
    attempts INTEGER NOT NULL DEFAULT 0, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX google_jobs_pending_idx ON google_jobs(next_attempt_at) WHERE status='PENDING';

CREATE TABLE djen_editions (
    id UUID PRIMARY KEY DEFAULT uuidv7(), tribunal TEXT NOT NULL, edition_on DATE NOT NULL,
    medium TEXT NOT NULL DEFAULT 'D', source_version TEXT NOT NULL DEFAULT '',
    source_hash TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'PENDING',
    item_count INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '',
    processed_at TIMESTAMPTZ, UNIQUE(tribunal, edition_on, medium, source_version)
);
CREATE TABLE recent_publication_matches (
    source_id TEXT NOT NULL, oab_uf CHAR(2) NOT NULL, oab_number TEXT NOT NULL,
    available_on DATE NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY(source_id,oab_uf,oab_number)
);

-- +goose Down
DROP TABLE recent_publication_matches;
DROP TABLE djen_editions;
DROP TABLE google_jobs;
DROP TABLE google_events;
DROP TABLE google_connections;
DROP TABLE google_oauth_states;
DROP TABLE deadlines;
DROP TABLE deadline_suggestions;
DROP TABLE court_calendar_days;
DROP TABLE court_calendar_versions;
DROP TABLE case_publications;
DROP TABLE user_publications;
DROP TABLE publications;
DROP TABLE cases;
DROP TABLE password_resets;
DROP INDEX users_oab_unique;
ALTER TABLE users DROP COLUMN monitoring_started_at, DROP COLUMN llm_consent,
    DROP COLUMN labor_practice, DROP COLUMN active, DROP COLUMN role,
    DROP COLUMN oab_uf, DROP COLUMN oab_number, DROP COLUMN name;
