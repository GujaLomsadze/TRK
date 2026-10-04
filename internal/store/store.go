// Package store persists the append-only event log and materialized state in SQLite.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/GujaLomsadze/trk/internal/model"
	_ "modernc.org/sqlite"
)

var migrations = []string{`
CREATE TABLE events(
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  session_id TEXT,
  source TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  attribution TEXT
);
CREATE INDEX events_session_ts ON events(session_id, ts);
CREATE INDEX events_ts ON events(ts);
CREATE TABLE sessions(
  session_id TEXT PRIMARY KEY,
  name TEXT, cwd TEXT, repo TEXT, branch TEXT, model TEXT,
  task TEXT, step_text TEXT, step_i INTEGER, step_n INTEGER,
  status TEXT,
  ctx_pct REAL, ctx_size INTEGER, cost_usd REAL, tokens_in INTEGER, tokens_out INTEGER,
  started_at INTEGER, last_event_at INTEGER
);
CREATE TABLE account(
  id INTEGER PRIMARY KEY CHECK (id = 1),
  five_h_pct REAL, five_h_reset INTEGER, seven_d_pct REAL, seven_d_reset INTEGER, updated_at INTEGER
);`, `
ALTER TABLE sessions ADD COLUMN dismissed_at INTEGER;
CREATE TABLE settings(key TEXT PRIMARY KEY, value TEXT NOT NULL);`, `
CREATE TABLE claude_pids(pid INTEGER PRIMARY KEY, session_id TEXT NOT NULL, seen_at INTEGER NOT NULL);`, `
ALTER TABLE sessions ADD COLUMN last_prompt TEXT;
ALTER TABLE sessions ADD COLUMN last_prompt_at INTEGER;
ALTER TABLE sessions ADD COLUMN last_reply TEXT;
ALTER TABLE sessions ADD COLUMN last_reply_at INTEGER;
ALTER TABLE sessions ADD COLUMN lines_added INTEGER;
ALTER TABLE sessions ADD COLUMN lines_removed INTEGER;
ALTER TABLE sessions ADD COLUMN api_ms INTEGER;
ALTER TABLE sessions ADD COLUMN wall_ms INTEGER;
ALTER TABLE sessions ADD COLUMN subagents TEXT;
ALTER TABLE sessions ADD COLUMN bg_running INTEGER;`}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer; SQLite serializes anyway
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Append(e *model.Event) error {
	res, err := s.db.Exec(`INSERT INTO events(ts, session_id, source, kind, payload, attribution) VALUES(?,?,?,?,?,?)`,
		e.TS, e.SessionID, e.Source, e.Kind, string(e.Payload), e.Attribution)
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

const eventCols = `id, ts, session_id, source, kind, payload, attribution`

func scanEvents(rows *sql.Rows) ([]model.Event, error) {
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		var sid, attr sql.NullString
		var payload string
		if err := rows.Scan(&e.ID, &e.TS, &sid, &e.Source, &e.Kind, &payload, &attr); err != nil {
			return nil, err
		}
		e.SessionID, e.Attribution, e.Payload = sid.String, attr.String, json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventsSince returns events with ts >= ms in log order.
func (s *Store) EventsSince(ms int64) ([]model.Event, error) {
	rows, err := s.db.Query(`SELECT `+eventCols+` FROM events WHERE ts >= ? ORDER BY ts, id`, ms)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// SessionEvents returns the session's most recent events, oldest first.
func (s *Store) SessionEvents(id string, limit int) ([]model.Event, error) {
	rows, err := s.db.Query(`SELECT * FROM (SELECT `+eventCols+` FROM events WHERE session_id = ? ORDER BY ts DESC, id DESC LIMIT ?) ORDER BY ts, id`, id, limit)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

func (s *Store) UpsertSession(x model.Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions(session_id, name, cwd, repo, branch, model, task, step_text, step_i, step_n, status,
  ctx_pct, ctx_size, cost_usd, tokens_in, tokens_out, started_at, last_event_at, dismissed_at,
  last_prompt, last_prompt_at, last_reply, last_reply_at, lines_added, lines_removed, api_ms, wall_ms, subagents, bg_running)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(session_id) DO UPDATE SET name=excluded.name, cwd=excluded.cwd, repo=excluded.repo, branch=excluded.branch,
  model=excluded.model, task=excluded.task, step_text=excluded.step_text, step_i=excluded.step_i, step_n=excluded.step_n,
  status=excluded.status, ctx_pct=excluded.ctx_pct, ctx_size=excluded.ctx_size, cost_usd=excluded.cost_usd,
  tokens_in=excluded.tokens_in, tokens_out=excluded.tokens_out, started_at=excluded.started_at, last_event_at=excluded.last_event_at,
  dismissed_at=excluded.dismissed_at, last_prompt=excluded.last_prompt, last_prompt_at=excluded.last_prompt_at,
  last_reply=excluded.last_reply, last_reply_at=excluded.last_reply_at, lines_added=excluded.lines_added,
  lines_removed=excluded.lines_removed, api_ms=excluded.api_ms, wall_ms=excluded.wall_ms, subagents=excluded.subagents,
  bg_running=excluded.bg_running`,
		x.SessionID, x.Name, x.Cwd, x.Repo, x.Branch, x.Model, x.Task, x.StepText, x.StepI, x.StepN, x.Status,
		x.CtxPct, x.CtxSize, x.CostUSD, x.TokensIn, x.TokensOut, x.StartedAt, x.LastEventAt, x.DismissedAt,
		x.LastPrompt, x.LastPromptAt, x.LastReply, x.LastReplyAt, x.LinesAdded, x.LinesRemoved, x.APIMs, x.WallMs,
		subagentsJSON(x.Subagents), x.BgRunning)
	return err
}

func subagentsJSON(m map[string]int) any {
	if len(m) == 0 {
		return nil
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func (s *Store) Sessions() ([]model.Session, error) {
	rows, err := s.db.Query(`SELECT session_id, coalesce(name,''), coalesce(cwd,''), coalesce(repo,''), coalesce(branch,''),
  coalesce(model,''), coalesce(task,''), coalesce(step_text,''), coalesce(step_i,0), coalesce(step_n,0), coalesce(status,''),
  ctx_pct, coalesce(ctx_size,0), coalesce(cost_usd,0), coalesce(tokens_in,0), coalesce(tokens_out,0),
  coalesce(started_at,0), coalesce(last_event_at,0), coalesce(dismissed_at,0),
  coalesce(last_prompt,''), coalesce(last_prompt_at,0), coalesce(last_reply,''), coalesce(last_reply_at,0),
  coalesce(lines_added,0), coalesce(lines_removed,0), coalesce(api_ms,0), coalesce(wall_ms,0), coalesce(subagents,''),
  coalesce(bg_running,0) FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		var x model.Session
		var subs string
		if err := rows.Scan(&x.SessionID, &x.Name, &x.Cwd, &x.Repo, &x.Branch, &x.Model, &x.Task, &x.StepText,
			&x.StepI, &x.StepN, &x.Status, &x.CtxPct, &x.CtxSize, &x.CostUSD, &x.TokensIn, &x.TokensOut,
			&x.StartedAt, &x.LastEventAt, &x.DismissedAt,
			&x.LastPrompt, &x.LastPromptAt, &x.LastReply, &x.LastReplyAt, &x.LinesAdded, &x.LinesRemoved, &x.APIMs, &x.WallMs,
			&subs, &x.BgRunning); err != nil {
			return nil, err
		}
		if subs != "" {
			_ = json.Unmarshal([]byte(subs), &x.Subagents)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SaveAccount(a model.Account) error {
	_, err := s.db.Exec(`INSERT INTO account(id, five_h_pct, five_h_reset, seven_d_pct, seven_d_reset, updated_at) VALUES(1,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET five_h_pct=excluded.five_h_pct, five_h_reset=excluded.five_h_reset,
  seven_d_pct=excluded.seven_d_pct, seven_d_reset=excluded.seven_d_reset, updated_at=excluded.updated_at`,
		a.FiveHPct, a.FiveHReset, a.SevenDPct, a.SevenDReset, a.UpdatedAt)
	return err
}

func (s *Store) Account() (model.Account, error) {
	var a model.Account
	err := s.db.QueryRow(`SELECT five_h_pct, coalesce(five_h_reset,0), seven_d_pct, coalesce(seven_d_reset,0), coalesce(updated_at,0) FROM account WHERE id = 1`).
		Scan(&a.FiveHPct, &a.FiveHReset, &a.SevenDPct, &a.SevenDReset, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.Account{}, nil
	}
	return a, err
}

// LimitPoint is one plan-usage reading: unix ms and percent used.
type LimitPoint struct {
	TS  int64   `json:"ts"`
	Pct float64 `json:"pct"`
}

var limitPaths = map[string]string{
	"five_hour": "$.rate_limits.five_hour.used_percentage",
	"seven_day": "$.rate_limits.seven_day.used_percentage",
}

// LimitHistory reads one plan-usage window ("five_hour" or "seven_day") back out of the
// stored statusline payloads from since on, keeping the highest reading per bucket ms.
func (s *Store) LimitHistory(window string, since, bucket int64) ([]LimitPoint, error) {
	path, ok := limitPaths[window]
	if !ok {
		return nil, fmt.Errorf("unknown limit window %q", window)
	}
	rows, err := s.db.Query(`SELECT max(ts), max(p) FROM (
  SELECT ts, json_extract(payload, ?) AS p FROM events WHERE source = 'statusline' AND ts >= ?
) WHERE p IS NOT NULL GROUP BY ts / ? ORDER BY 1`, path, since, bucket)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LimitPoint{}
	for rows.Next() {
		var p LimitPoint
		if err := rows.Scan(&p.TS, &p.Pct); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Setting reads one daemon setting; ok is false when it was never set.
func (s *Store) Setting(key string) (string, bool, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return v, err == nil, err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// UsedBytes is the space the database actually occupies: pages in use, not
// free pages left behind by deletes (SQLite reuses those for new rows).
func (s *Store) UsedBytes() (int64, error) {
	var pages, free, size int64
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		return 0, err
	}
	if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&free); err != nil {
		return 0, err
	}
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&size); err != nil {
		return 0, err
	}
	return (pages - free) * size, nil
}

// TrimToSize deletes the oldest raw events once the database uses more than max
// bytes, down to 90% of max, so it stops growing. Session and account rows are
// never touched. Returns how many events were removed.
func (s *Store) TrimToSize(max int64) (int64, error) {
	used, err := s.UsedBytes()
	if err != nil || used <= max {
		return 0, err
	}
	target := max / 10 * 9
	var rows int64
	if err := s.db.QueryRow(`SELECT count(*) FROM events`).Scan(&rows); err != nil {
		return 0, err
	}
	batch := rows / 20 // trim in 5% steps so we stop close to the target
	if batch < 50 {
		batch = 50
	}
	var total int64
	for used > target {
		res, err := s.db.Exec(`DELETE FROM events WHERE id IN (SELECT id FROM events ORDER BY id LIMIT ?)`, batch)
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			break // nothing left to trim
		}
		total += n
		if used, err = s.UsedBytes(); err != nil {
			return total, err
		}
	}
	return total, nil
}

// SavePID remembers which session a Claude process belongs to, so CLI calls
// from that process keep their attribution across daemon restarts.
func (s *Store) SavePID(pid int, sessionID string, ts int64) error {
	_, err := s.db.Exec(`INSERT INTO claude_pids(pid, session_id, seen_at) VALUES(?,?,?)
ON CONFLICT(pid) DO UPDATE SET session_id = excluded.session_id, seen_at = excluded.seen_at`, pid, sessionID, ts)
	return err
}

// PIDs returns the pid → session mappings seen since the given time (unix ms).
func (s *Store) PIDs(since int64) (map[int]string, error) {
	rows, err := s.db.Query(`SELECT pid, session_id FROM claude_pids WHERE seen_at >= ?`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var pid int
		var sid string
		if err := rows.Scan(&pid, &sid); err != nil {
			return nil, err
		}
		out[pid] = sid
	}
	return out, rows.Err()
}
