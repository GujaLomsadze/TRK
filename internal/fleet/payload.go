package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
)

// payload is the union of fields TRK reads from CLI, hook and status line JSON.
// Everything is optional; unknown fields are ignored; type mismatches skip the field.
type payload struct {
	// CLI verbs
	Text  string `json:"text"`
	Steps int    `json:"steps"`
	I     int    `json:"i"`
	N     int    `json:"n"`
	// shared
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	Model     json.RawMessage `json:"model"` // string (SessionStart) or object (status line)
	// hooks
	HookEvent        string          `json:"hook_event_name"`
	ToolName         string          `json:"tool_name"`
	ToolInput        json.RawMessage `json:"tool_input"`
	ToolUseID        string          `json:"tool_use_id"`
	Message          string          `json:"message"`
	NotificationType string          `json:"notification_type"`
	// status line
	Workspace *struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cost *struct {
		TotalCostUSD *float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow *struct {
		UsedPercentage    *float64 `json:"used_percentage"`
		ContextWindowSize *float64 `json:"context_window_size"`
		TotalInputTokens  *float64 `json:"total_input_tokens"`
		TotalOutputTokens *float64 `json:"total_output_tokens"`
	} `json:"context_window"`
	RateLimits *struct {
		FiveHour *window `json:"five_hour"`
		SevenDay *window `json:"seven_day"`
	} `json:"rate_limits"`
}

type window struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"` // unix seconds
}

func parse(raw []byte) payload {
	var p payload
	err := json.Unmarshal(raw, &p)
	var typeErr *json.UnmarshalTypeError
	if err != nil && !errors.As(err, &typeErr) {
		return payload{} // syntax error: nothing usable
	}
	return p // on a type error, encoding/json still fills every other field
}

func (p payload) cwd() string {
	if p.Cwd != "" {
		return p.Cwd
	}
	if p.Workspace != nil {
		return p.Workspace.CurrentDir
	}
	return ""
}

func (p payload) modelName() string {
	m := bytes.TrimSpace(p.Model)
	if len(m) == 0 {
		return ""
	}
	if m[0] == '"' {
		var s string
		_ = json.Unmarshal(m, &s)
		return s
	}
	var o struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	}
	_ = json.Unmarshal(m, &o)
	if o.DisplayName != "" {
		return o.DisplayName
	}
	return o.ID
}

const (
	trimAbove  = 16 << 10
	maxStrRune = 2048
)

// trimPayload keeps stored payloads small: Write contents and tool responses can be megabytes.
func trimPayload(raw json.RawMessage) json.RawMessage {
	if len(raw) <= trimAbove {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return json.RawMessage(`{}`)
	}
	limit := maxStrRune
	for {
		out, err := json.Marshal(shrink(v, limit))
		if err != nil {
			return json.RawMessage(`{}`)
		}
		if len(out) <= trimAbove || limit <= 64 {
			return out
		}
		limit /= 4 // many medium strings: tighten until it fits
	}
}

func shrink(v any, limit int) any {
	switch t := v.(type) {
	case string:
		if r := []rune(t); len(r) > limit {
			return string(r[:limit]) + "…[trimmed]"
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = shrink(x, limit)
		}
		return out
	case []any:
		if len(t) > 200 {
			t = t[:200]
		}
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = shrink(x, limit)
		}
		return out
	}
	return v
}
