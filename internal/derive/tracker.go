// Package derive holds TRK's pure heuristics: status, loops, collisions, context advice.
package derive

import "sort"

const (
	IdleAfter       int64 = 2 * 60 * 1000
	LoopWindow      int64 = 10 * 60 * 1000
	CollisionWindow int64 = 10 * 60 * 1000
	KeepWindow      int64 = 30 * 60 * 1000 // tool calls kept for loop detection + timeline
	LoopFailures          = 5
	ChurnEdits            = 8
)

type ToolCall struct {
	ID      string `json:"-"` // tool_use_id
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Key     string `json:"-"` // tool + normalized input
	File    string `json:"file,omitempty"`
	Edit    bool   `json:"edit,omitempty"`
	Start   int64  `json:"start"`
	End     int64  `json:"end,omitempty"` // 0 while running
	Failed  bool   `json:"failed,omitempty"`
}

type Need struct {
	Type string `json:"type"` // permission|question|blocked
	Text string `json:"text"`
	TS   int64  `json:"ts"`
	Weak bool   `json:"-"` // from a Notification; a PermissionRequest has better text
}

type Span struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"` // 0 while open
}

// Tracker is the per-session working memory the heuristics read.
// Hooks run async, so events can arrive out of order: every mutation compares timestamps.
type Tracker struct {
	ProgressAt   int64 // last start/step/progress
	Blocked      *Need // declared via trk blocked
	Pending      *Need // permission prompt / question awaiting the human
	LastClear    int64 // latest event that resolves Pending
	DoneAt       int64 // done/stop/session_end
	LastActivity int64
	Calls        []ToolCall
	Waits        []Span
}

func (t *Tracker) Activity(ts int64) {
	if ts > t.LastActivity {
		t.LastActivity = ts
	}
}

func (t *Tracker) SetPending(n Need) {
	if n.TS <= t.LastClear {
		return // already resolved by a later event that arrived first
	}
	if t.Pending != nil {
		if t.Pending.Weak && !n.Weak {
			t.Pending.Text, t.Pending.Type, t.Pending.Weak = n.Text, n.Type, false
		}
		return
	}
	t.Pending = &n
	t.Waits = append(t.Waits, Span{Start: n.TS})
}

func (t *Tracker) ClearPending(ts int64) {
	if ts > t.LastClear {
		t.LastClear = ts
	}
	if t.Pending == nil || ts < t.Pending.TS {
		return
	}
	if n := len(t.Waits); n > 0 && t.Waits[n-1].End == 0 {
		t.Waits[n-1].End = ts
	}
	t.Pending = nil
}

func (t *Tracker) find(id string) int {
	if id == "" {
		return -1
	}
	for i := range t.Calls {
		if t.Calls[i].ID == id {
			return i
		}
	}
	return -1
}

func (t *Tracker) insert(c ToolCall) {
	i := sort.Search(len(t.Calls), func(i int) bool { return t.Calls[i].Start > c.Start })
	t.Calls = append(t.Calls, ToolCall{})
	copy(t.Calls[i+1:], t.Calls[i:])
	t.Calls[i] = c
}

func (t *Tracker) ToolStart(c ToolCall) {
	if i := t.find(c.ID); i >= 0 {
		if c.Start < t.Calls[i].Start { // post arrived first and guessed Start=End
			t.Calls[i].Start = c.Start
			sort.SliceStable(t.Calls, func(a, b int) bool { return t.Calls[a].Start < t.Calls[b].Start })
		}
		return
	}
	t.insert(c)
}

func (t *Tracker) ToolEnd(c ToolCall) {
	if i := t.find(c.ID); i >= 0 {
		t.Calls[i].End, t.Calls[i].Failed = c.End, c.Failed
		return
	}
	if c.Start == 0 {
		c.Start = c.End
	}
	t.insert(c)
}

func (t *Tracker) Prune(now int64) {
	cut := now - KeepWindow
	i := 0
	for i < len(t.Calls) && t.Calls[i].Start < cut {
		i++
	}
	t.Calls = t.Calls[i:]
	w := t.Waits[:0]
	for _, s := range t.Waits {
		if s.End == 0 || s.End >= cut {
			w = append(w, s)
		}
	}
	t.Waits = w
}
