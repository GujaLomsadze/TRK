package store

import "database/sql"

// StatRow is one event cut down to the fields the Stats page reads. Status-line rows carry the
// cumulative counters (cost, model time, tokens) and context use; tool rows carry the tool, its
// id, and the command or file it worked on. Missing fields are zero / empty.
type StatRow struct {
	TS        int64
	Session   string
	Kind      string
	Tool      string
	ToolUseID string
	Command   string
	File      string
	Cost      float64 // cumulative for the claude process, USD
	APIMs     float64 // cumulative model time, ms
	Tokens    float64 // cumulative input + output tokens
	Ctx       float64 // context used, %
	HasStatus bool    // the status-line counters above are present
	HasCtx    bool
}

// statKinds are the event kinds the Stats page uses.
const statKinds = `('status','tool_pre','tool_post','tool_fail','permission','notification','prompt','stop','session_end')`

const statCols = `ts, coalesce(session_id,''), kind,
  json_extract(payload,'$.tool_name'), json_extract(payload,'$.tool_use_id'),
  json_extract(payload,'$.tool_input.command'),
  coalesce(json_extract(payload,'$.tool_input.file_path'), json_extract(payload,'$.tool_input.notebook_path')),
  json_extract(payload,'$.cost.total_cost_usd'), json_extract(payload,'$.cost.total_api_duration_ms'),
  json_extract(payload,'$.context_window.total_input_tokens'), json_extract(payload,'$.context_window.total_output_tokens'),
  json_extract(payload,'$.context_window.used_percentage')`

// StatRows returns the Stats page's events in [since, until), oldest first.
func (s *Store) StatRows(since, until int64) ([]StatRow, error) {
	rows, err := s.db.Query(`SELECT `+statCols+` FROM events WHERE ts >= ? AND ts < ? AND kind IN `+statKinds+` ORDER BY ts, id`, since, until)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StatRow
	for rows.Next() {
		r, err := scanStat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LastStatus is a session's latest status-line reading before ts: the baseline its counters
// grew from (they are cumulative per claude process).
func (s *Store) LastStatus(session string, before int64) (StatRow, bool, error) {
	row := s.db.QueryRow(`SELECT `+statCols+` FROM events WHERE session_id = ? AND ts < ? AND kind = 'status'
  AND json_extract(payload,'$.cost.total_cost_usd') IS NOT NULL ORDER BY ts DESC, id DESC LIMIT 1`, session, before)
	r, err := scanStat(row)
	if err == sql.ErrNoRows {
		return StatRow{}, false, nil
	}
	return r, err == nil, err
}

type scanner interface{ Scan(...any) error }

func scanStat(sc scanner) (StatRow, error) {
	var r StatRow
	var tool, id, cmd, file sql.NullString
	var cost, api, tin, tout, ctx sql.NullFloat64
	if err := sc.Scan(&r.TS, &r.Session, &r.Kind, &tool, &id, &cmd, &file, &cost, &api, &tin, &tout, &ctx); err != nil {
		return r, err
	}
	r.Tool, r.ToolUseID, r.Command, r.File = tool.String, id.String, cmd.String, file.String
	if cost.Valid {
		r.HasStatus = true
		r.Cost, r.APIMs, r.Tokens = cost.Float64, api.Float64, tin.Float64+tout.Float64
	}
	if ctx.Valid {
		r.HasCtx, r.Ctx = true, ctx.Float64
	}
	return r, nil
}
