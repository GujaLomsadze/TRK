package derive

import (
	"fmt"
	"path/filepath"
	"sort"
)

const (
	StatusWorking = "working"
	StatusWaiting = "waiting"
	StatusBlocked = "blocked"
	StatusIdle    = "idle"
	StatusLooping = "looping"
	StatusDone    = "done"
)

type Loop struct {
	Kind    string `json:"kind"` // fail|churn
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Desc    string `json:"desc"`
	Count   int    `json:"count"`
	FirstTS int64  `json:"first_ts"`
	LastTS  int64  `json:"last_ts"`
}

// Status applies spec §6.3. Precedence: waiting > blocked > done > idle > looping > working.
// The loop is returned even when another status wins, so the UI can still show the reality box.
func Status(t *Tracker, now int64) (string, *Loop) {
	loop := DetectLoop(t.Calls, t.ProgressAt, now)
	switch {
	case t.Pending != nil:
		return StatusWaiting, loop
	case t.Blocked != nil:
		return StatusBlocked, loop
	case t.DoneAt > 0 && t.LastActivity <= t.DoneAt:
		return StatusDone, loop
	case now-t.LastActivity > IdleAfter:
		return StatusIdle, loop
	case loop != nil:
		return StatusLooping, loop
	}
	return StatusWorking, nil
}

// DetectLoop finds the worst repetition in the last LoopWindow: the same tool+input
// failing ≥ LoopFailures times, or one file edited ≥ ChurnEdits times since the
// last declared progress.
func DetectLoop(calls []ToolCall, progressAt, now int64) *Loop {
	since := now - LoopWindow
	fails := map[string]*Loop{}
	churn := map[string]*Loop{}
	bump := func(m map[string]*Loop, k, kind string, c ToolCall) {
		l := m[k]
		if l == nil {
			l = &Loop{Kind: kind, Tool: c.Tool, Summary: c.Summary, FirstTS: c.Start}
			m[k] = l
		}
		l.Count++
		end := c.End
		if end == 0 {
			end = c.Start
		}
		if end > l.LastTS {
			l.LastTS = end
		}
	}
	for _, c := range calls {
		if c.Start < since {
			continue
		}
		if c.Failed && c.Key != "" {
			bump(fails, c.Key, "fail", c)
		}
		if c.Edit && c.File != "" && c.Start > progressAt {
			bump(churn, c.File, "churn", c)
		}
	}
	if l := worst(fails, LoopFailures); l != nil {
		l.Desc = fmt.Sprintf("%s `%s` failed %d× in %s", l.Tool, l.Summary, l.Count, span(l.LastTS-l.FirstTS))
		return l
	}
	if l := worst(churn, ChurnEdits); l != nil {
		l.Desc = fmt.Sprintf("%s edited %d× in %s with no progress update", filepath.Base(l.Summary), l.Count, span(l.LastTS-l.FirstTS))
		return l
	}
	return nil
}

func worst(m map[string]*Loop, threshold int) *Loop {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic tie-break
	var best *Loop
	for _, k := range keys {
		if l := m[k]; l.Count >= threshold && (best == nil || l.Count > best.Count) {
			best = l
		}
	}
	return best
}

func span(ms int64) string {
	if ms < 60*1000 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%d min", ms/60000)
}
