// Package model defines the wire envelope, stored events and materialized state.
package model

import "encoding/json"

// Envelope is what clients POST to /v1/events.
type Envelope struct {
	TS         int64           `json:"ts,omitempty"`          // unix ms at the caller
	Source     string          `json:"source"`                // cli|hook|statusline|api
	Kind       string          `json:"kind,omitempty"`        // empty for hook/statusline: derived by the daemon
	SessionID  string          `json:"session_id,omitempty"`  // explicit session (TRK_SESSION or third-party)
	SessionVia string          `json:"session_via,omitempty"` // "env" when SessionID came from TRK_SESSION
	ClaudePID  int             `json:"claude_pid,omitempty"`  // nearest ancestor claude process, 0 if none
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type Event struct {
	ID          int64           `json:"id"`
	TS          int64           `json:"ts"`
	SessionID   string          `json:"session_id"`
	Source      string          `json:"source"`
	Kind        string          `json:"kind"`
	Payload     json.RawMessage `json:"payload"`
	Attribution string          `json:"attribution"`
}

type Session struct {
	SessionID   string   `json:"session_id"`
	Name        string   `json:"name"`
	Cwd         string   `json:"cwd"`
	Repo        string   `json:"repo"`
	Branch      string   `json:"branch"`
	Model       string   `json:"model"`
	Task        string   `json:"task"`
	StepText    string   `json:"step_text"`
	StepI       int      `json:"step_i"`
	StepN       int      `json:"step_n"`
	Status      string   `json:"status"`
	CtxPct      *float64 `json:"ctx_pct"`
	CtxSize     int64    `json:"ctx_size"`
	CostUSD     float64  `json:"cost_usd"`
	TokensIn    int64    `json:"tokens_in"`
	TokensOut   int64    `json:"tokens_out"`
	StartedAt   int64    `json:"started_at"`
	LastEventAt int64    `json:"last_event_at"`
	DismissedAt int64    `json:"-"` // ✕ on the card; hidden until new activity after this time
	// Card widgets, from hooks and the status line.
	LastPrompt   string         `json:"last_prompt,omitempty"`
	LastPromptAt int64          `json:"last_prompt_at,omitempty"`
	LastReply    string         `json:"last_reply,omitempty"` // main agent's last message (Stop hook)
	LastReplyAt  int64          `json:"last_reply_at,omitempty"`
	LinesAdded   int64          `json:"lines_added"`
	LinesRemoved int64          `json:"lines_removed"`
	APIMs        int64          `json:"api_ms"`              // time the model was working
	WallMs       int64          `json:"wall_ms"`             // session wall time
	Subagents    map[string]int `json:"subagents,omitempty"` // finished subagents by type; replaced, never mutated
	BgRunning    int            `json:"bg_running"`          // background tasks still running at the last Stop
}

// Account is the latest plan-limit snapshot. Resets are unix ms.
type Account struct {
	FiveHPct    *float64 `json:"five_h_pct"`
	FiveHReset  int64    `json:"five_h_reset"`
	SevenDPct   *float64 `json:"seven_d_pct"`
	SevenDReset int64    `json:"seven_d_reset"`
	UpdatedAt   int64    `json:"updated_at"`
}

const (
	SourceCLI        = "cli"
	SourceHook       = "hook"
	SourceStatusline = "statusline"
	SourceAPI        = "api"
)

const (
	AttrEnv    = "env"
	AttrPtree  = "ptree"
	AttrCwd    = "cwd"
	AttrNative = "native"
)

const (
	KindStart        = "start"
	KindStep         = "step"
	KindProgress     = "progress"
	KindBlocked      = "blocked"
	KindDone         = "done"
	KindSessionStart = "session_start"
	KindSessionEnd   = "session_end"
	KindPrompt       = "prompt"
	KindToolPre      = "tool_pre"
	KindToolPost     = "tool_post"
	KindToolFail     = "tool_fail"
	KindPermission   = "permission"
	KindNotification = "notification"
	KindStop         = "stop"
	KindSubagentStop = "subagent_stop"
	KindCompact      = "compact"
	KindStatus       = "status"
	KindHookOther    = "hook"
	KindOther        = "event"
)

// HookEvents are the Claude Code hook events trk init registers.
var HookEvents = []string{
	"SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse",
	"PostToolUseFailure", "PermissionRequest", "Notification", "Stop", "SubagentStop", "PreCompact",
}

var hookKinds = map[string]string{
	"SessionStart":       KindSessionStart,
	"SessionEnd":         KindSessionEnd,
	"UserPromptSubmit":   KindPrompt,
	"PreToolUse":         KindToolPre,
	"PostToolUse":        KindToolPost,
	"PostToolUseFailure": KindToolFail,
	"PermissionRequest":  KindPermission,
	"Notification":       KindNotification,
	"Stop":               KindStop,
	"SubagentStop":       KindSubagentStop,
	"PreCompact":         KindCompact,
}

func KindForHook(event string) string {
	if k, ok := hookKinds[event]; ok {
		return k
	}
	return KindHookOther
}

// IsActivity reports whether an event kind means the agent is doing something
// (as opposed to bookkeeping: status updates, notifications, turn ends).
func IsActivity(kind string) bool {
	switch kind {
	case KindStart, KindStep, KindProgress, KindBlocked, KindDone, KindSessionStart, KindPrompt,
		KindToolPre, KindToolPost, KindToolFail, KindPermission, KindCompact, KindSubagentStop:
		return true
	}
	return false
}
