package fleet

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/derive"
	"github.com/GujaLomsadze/trk/internal/model"
)

const (
	timelineSpan  = 30 * 60 * 1000
	staleProgress = 15 * 60 * 1000
	staleCalls    = 25
)

type CallView struct {
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	State   string `json:"state"` // ok|fail|run
}

type Block struct {
	Start int64  `json:"start"`
	End   int64  `json:"end"` // 0 = still running
	Cat   string `json:"cat"` // read|edit|run|wait
}

type SessionView struct {
	model.Session
	Loop      *derive.Loop      `json:"loop,omitempty"`
	Reality   string            `json:"reality,omitempty"`
	Recent    []CallView        `json:"recent"`
	Timeline  []Block           `json:"timeline"`
	Attention bool              `json:"attention"`
	AutoName  string            `json:"auto_name,omitempty"` // the name TRK would show, when a title replaces it
	Need      *derive.Need      `json:"need,omitempty"`
	Ctx       derive.CtxBand    `json:"ctx"`
	Calls     []derive.ToolCall `json:"calls,omitempty"`
	// Activity in the tracker window (last 30 min), for the optional card widgets.
	ToolCalls   int `json:"tool_calls"`
	FilesRead   int `json:"files_read"` // read but never edited
	FilesEdited int `json:"files_edited"`
}

type NeedItem struct {
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
	derive.Need
}

type CollisionView struct {
	derive.Collision
	Names []string `json:"names"`
}

type Stats struct {
	Agents      int   `json:"agents"`
	Working     int   `json:"working"`
	NeedsYou    int   `json:"needs_you"`
	TokensToday int64 `json:"tokens_today"`
}

type View struct {
	Now        int64           `json:"now"`
	Sessions   []SessionView   `json:"sessions"`
	Needs      []NeedItem      `json:"needs"`
	Collisions []CollisionView `json:"collisions"`
	Account    model.Account   `json:"account"`
	Stats      Stats           `json:"stats"`
	// HideAfterMin is the auto-hide setting in minutes (0 = never), shown in the layout drawer.
	HideAfterMin int `json:"hide_after_min"`
}

func (f *Fleet) View(now int64) View {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := View{Now: now, Account: f.account, Sessions: []SessionView{}, Needs: []NeedItem{}, Collisions: []CollisionView{}, HideAfterMin: int(f.hideAfter / 60000)}
	var touches []derive.FileTouch
	for _, e := range f.sessions {
		sv := buildView(e, now)
		if f.hidden(e, sv.Status, now) {
			continue
		}
		v.Sessions = append(v.Sessions, sv)
		if sv.Status != derive.StatusDone {
			for _, c := range e.T.Calls {
				if c.File != "" {
					touches = append(touches, derive.FileTouch{Session: sv.SessionID, Path: c.File, TS: c.Start, Edit: c.Edit})
				}
			}
		}
	}
	disambiguate(v.Sessions)
	// Fixed slots: order by session start so cards never jump around.
	// Attention is shown by glow and the Needs-you inbox, not by position.
	sort.Slice(v.Sessions, func(i, j int) bool {
		a, b := v.Sessions[i], v.Sessions[j]
		if a.StartedAt != b.StartedAt {
			return a.StartedAt < b.StartedAt
		}
		return a.SessionID < b.SessionID
	})

	names := map[string]string{}
	midnight := localMidnight(now)
	for _, s := range v.Sessions {
		names[s.SessionID] = s.Name
		v.Stats.Agents++
		if s.Status == derive.StatusWorking {
			v.Stats.Working++
		}
		if s.LastEventAt >= midnight {
			v.Stats.TokensToday += s.TokensIn + s.TokensOut
		}
		if s.Need != nil {
			v.Needs = append(v.Needs, NeedItem{SessionID: s.SessionID, Name: s.Name, Need: *s.Need})
		}
	}
	sort.Slice(v.Needs, func(i, j int) bool { return v.Needs[i].TS < v.Needs[j].TS }) // oldest first
	v.Stats.NeedsYou = len(v.Needs)
	for _, c := range derive.Collisions(touches, now) {
		cv := CollisionView{Collision: c}
		for _, sid := range c.Sessions {
			cv.Names = append(cv.Names, names[sid])
		}
		v.Collisions = append(v.Collisions, cv)
	}
	return v
}

// hidden: dismissed with no activity since, or idle/done longer than the auto-hide setting.
// Activity (not LastEventAt) is the clock: status-line refreshes keep arriving for idle sessions.
func (f *Fleet) hidden(e *entry, status string, now int64) bool {
	last := e.T.LastActivity
	if last == 0 {
		last = e.S.StartedAt
	}
	if e.S.DismissedAt > 0 && e.S.DismissedAt >= last {
		return true
	}
	quiet := status == derive.StatusDone || status == derive.StatusIdle
	return quiet && f.hideAfter > 0 && now-last > f.hideAfter
}

func (f *Fleet) SessionView(id string, now int64) (SessionView, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.sessions[id]
	if !ok {
		return SessionView{}, false
	}
	sv := buildView(e, now)
	sv.Calls = append([]derive.ToolCall(nil), e.T.Calls...)
	return sv, true
}

func buildView(e *entry, now int64) SessionView {
	t := &e.T
	sv := SessionView{Session: e.S, Recent: []CallView{}, Timeline: []Block{}}
	sv.Status, sv.Loop = derive.Status(t, now)
	sv.Ctx = derive.CtxAdvice(sv.CtxPct)
	if sv.Name == "" {
		sv.Name = slug(sv.Task, sv.SessionID)
	}
	if sv.Title != "" {
		sv.AutoName, sv.Name = sv.Name, sv.Title
	}
	switch {
	case t.Pending != nil:
		n := *t.Pending
		sv.Need = &n
	case t.Blocked != nil:
		n := *t.Blocked
		sv.Need = &n
	}
	sv.Attention = sv.Status == derive.StatusWaiting || sv.Status == derive.StatusBlocked || sv.Status == derive.StatusLooping
	if sv.Loop != nil {
		sv.Reality = sv.Loop.Desc
	} else if t.ProgressAt > 0 && now-t.ProgressAt > staleProgress && sv.Status == derive.StatusWorking {
		n := 0
		for _, c := range t.Calls {
			if c.Start > t.ProgressAt {
				n++
			}
		}
		if n >= staleCalls {
			sv.Reality = fmt.Sprintf("no progress report in %d min · %d tool calls since", (now-t.ProgressAt)/60000, n)
		}
	}
	edited, read := map[string]bool{}, map[string]bool{}
	for _, c := range t.Calls {
		if c.File == "" {
			continue
		}
		if c.Edit {
			edited[c.File] = true
		} else {
			read[c.File] = true
		}
	}
	for f := range read {
		if !edited[f] {
			sv.FilesRead++
		}
	}
	sv.ToolCalls, sv.FilesEdited = len(t.Calls), len(edited)
	for i := len(t.Calls) - 1; i >= 0 && len(sv.Recent) < 5; i-- {
		c := t.Calls[i]
		state := "run"
		if c.Failed {
			state = "fail"
		} else if c.End > 0 {
			state = "ok"
		}
		sv.Recent = append(sv.Recent, CallView{Tool: c.Tool, Summary: c.Summary, State: state})
	}
	from := now - timelineSpan
	for _, c := range t.Calls {
		end := c.End
		if (end == 0 && c.Start >= from) || end >= from {
			sv.Timeline = append(sv.Timeline, Block{Start: c.Start, End: end, Cat: derive.Category(c.Tool)})
		}
	}
	for _, w := range t.Waits {
		if w.End == 0 || w.End >= from {
			sv.Timeline = append(sv.Timeline, Block{Start: w.Start, End: w.End, Cat: "wait"})
		}
	}
	return sv
}

// disambiguate suffixes automatic names that collide; a chosen title is left as typed.
func disambiguate(ss []SessionView) {
	count := map[string]int{}
	for _, s := range ss {
		count[s.Name]++
	}
	for i := range ss {
		if count[ss[i].Name] > 1 && ss[i].Title == "" {
			id := ss[i].SessionID
			if len(id) > 4 {
				id = id[:4]
			}
			ss[i].Name += " #" + id
		}
	}
}

func slug(task, sid string) string {
	if task != "" {
		w := strings.Fields(strings.ToLower(task))
		if len(w) > 4 {
			w = w[:4]
		}
		return strings.Join(w, "-")
	}
	if len(sid) > 8 {
		return sid[:8]
	}
	return sid
}

func localMidnight(now int64) int64 {
	t := time.UnixMilli(now)
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, t.Location()).UnixMilli()
}
