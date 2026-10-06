// Package stats turns stored events into the numbers on the dashboard's Stats page: spend
// per day and repo, where session time goes, how long agents wait on you, when they work,
// tools, slow commands, files, context pressure and a sessions table. Compute is pure; the
// daemon feeds it rows from the store.
package stats

import (
	"math"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/derive"
	"github.com/GujaLomsadze/trk/internal/store"
)

const (
	day       = int64(24 * time.Hour / time.Millisecond)
	bucketMs  = int64(5 * time.Minute / time.Millisecond) // activity grain for agent hours and peak agents
	maxSpanMs = int64(time.Hour / time.Millisecond)       // one tool run or one wait counts at most this long in the time split
	topN      = 10
)

// Meta describes a session for display and filtering.
type Meta struct {
	Name, Repo, Model, Cwd string
}

// Input is everything Compute needs. Rows cover [PrevSince, Until): the extra period before
// Since only feeds the "vs previous period" spend. Prior holds each session's last status
// reading before PrevSince, the baseline its cumulative counters grow from.
type Input struct {
	Rows             []store.StatRow
	Prior            map[string]store.StatRow
	Meta             map[string]Meta
	PrevSince, Since int64
	Until            int64
	Days             int
	TZOffsetMin      int             // browser's getTimezoneOffset(): UTC minus local, minutes
	Repos            map[string]bool // only these repos; nil = all
}

type Result struct {
	Since    int64       `json:"since"`
	Until    int64       `json:"until"`
	Days     []Day       `json:"days"`
	Repos    []RepoTotal `json:"repos"` // every repo in range, unfiltered, biggest spend first
	KPI      KPI         `json:"kpi"`
	Waits    []float64   `json:"waits"` // seconds, ascending
	Heat     [7][24]int  `json:"heat"`  // tool calls by local weekday (Mon=0) and hour
	Tools    []Tool      `json:"tools"`
	Commands []Command   `json:"commands"`
	Files    []File      `json:"files"`
	Sessions []Session   `json:"sessions"` // most expensive first
}

type Day struct {
	Start int64              `json:"start"` // local midnight, unix ms
	Spend map[string]float64 `json:"spend"` // by repo
}

type RepoTotal struct {
	Repo  string  `json:"repo"`
	Spend float64 `json:"spend"`
}

type KPI struct {
	Spend      float64            `json:"spend"`
	SpendPrev  float64            `json:"spend_prev"`
	Tokens     float64            `json:"tokens"`
	ByModel    map[string]float64 `json:"tokens_by_model"`
	AgentHours float64            `json:"agent_hours"`
	PeakAgents int                `json:"peak_agents"`
	WaitMedian float64            `json:"wait_median"` // seconds; 0 when no waits
	WaitP90    float64            `json:"wait_p90"`
	ToolCalls  int                `json:"tool_calls"`
	ToolFails  int                `json:"tool_fails"`
}

type Tool struct {
	Name  string `json:"name"`
	Calls int    `json:"calls"`
	Fails int    `json:"fails"`
}

type Command struct {
	Cmd      string `json:"cmd"`
	Runs     int    `json:"runs"`
	MedianMs int64  `json:"median_ms"`
	P95Ms    int64  `json:"p95_ms"`
	Fails    int    `json:"fails"`
}

type File struct {
	Path   string `json:"path"`
	Reads  int    `json:"reads"`
	Edits  int    `json:"edits"`
	Agents int    `json:"agents"`
}

type Session struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Repo    string   `json:"repo"`
	Model   string   `json:"model"`
	First   int64    `json:"first"`
	Last    int64    `json:"last"`
	Spend   float64  `json:"spend"`
	Tokens  float64  `json:"tokens"`
	ModelMs int64    `json:"model_ms"`
	ToolMs  int64    `json:"tool_ms"`
	WaitMs  int64    `json:"wait_ms"`
	PeakCtx *float64 `json:"peak_ctx,omitempty"`
}

var editTools = map[string]bool{"Edit": true, "MultiEdit": true, "Write": true, "NotebookEdit": true}

// Compute builds the Stats page from in.
func Compute(in Input) Result {
	off := int64(in.TZOffsetMin) * 60000
	local := func(ts int64) int64 { return ts - off }
	dayIdx := func(ts int64) int { return int(floorDiv(local(ts), day) - floorDiv(local(in.Since), day)) }

	res := Result{Since: in.Since, Until: in.Until, Waits: []float64{}, Tools: []Tool{}, Commands: []Command{}, Files: []File{}, Sessions: []Session{},
		KPI: KPI{ByModel: map[string]float64{}}}
	for i := 0; i < in.Days; i++ {
		res.Days = append(res.Days, Day{Start: floorDiv(local(in.Since), day)*day + off + int64(i)*day, Spend: map[string]float64{}})
	}

	repoOf := func(sid string) string {
		m := in.Meta[sid]
		switch {
		case m.Repo != "":
			return m.Repo
		case m.Cwd != "":
			return path.Base(derive.NormPath(m.Cwd))
		case m.Name != "":
			return m.Name
		}
		return "other"
	}
	keep := func(sid string) bool { return in.Repos == nil || in.Repos[repoOf(sid)] }

	type acc struct {
		Session
		seen    bool
		ctxSeen bool
	}
	sess := map[string]*acc{}
	get := func(sid string) *acc {
		a := sess[sid]
		if a == nil {
			m := in.Meta[sid]
			a = &acc{Session: Session{ID: sid, Name: m.Name, Repo: repoOf(sid), Model: m.Model}}
			if a.Name == "" {
				a.Name = a.Repo
			}
			sess[sid] = a
		}
		return a
	}

	last := map[string]store.StatRow{} // previous status reading per session
	for sid, r := range in.Prior {
		last[sid] = r
	}
	repoSpend := map[string]float64{}
	type pending struct {
		ts        int64
		tool, cmd string
	}
	open := map[string]pending{} // tool_use_id → its PreToolUse
	waitFrom := map[string]int64{}
	cmdRuns := map[string][]int64{}
	cmdFails := map[string]int{}
	tools := map[string]*Tool{}
	type fileAcc struct {
		reads, edits int
		agents       map[string]bool
	}
	files := map[string]*fileAcc{}
	active := map[int64]map[string]bool{} // 5-min bucket → sessions busy in it

	for _, r := range in.Rows {
		sid := r.Session
		inRange := r.TS >= in.Since
		if r.Kind == "status" && r.HasStatus {
			prev := last[sid]
			last[sid] = r
			dCost, dAPI, dTok := grow(prev.Cost, r.Cost), grow(prev.APIMs, r.APIMs), grow(prev.Tokens, r.Tokens)
			if !inRange {
				if keep(sid) {
					res.KPI.SpendPrev += dCost
				}
				continue
			}
			repoSpend[repoOf(sid)] += dCost
			if !keep(sid) {
				continue
			}
			a := get(sid)
			a.Spend += dCost
			a.Tokens += dTok
			a.ModelMs += int64(dAPI)
			res.KPI.Spend += dCost
			res.KPI.Tokens += dTok
			res.KPI.ByModel[modelFamily(a.Model)] += dTok
			if i := dayIdx(r.TS); i >= 0 && i < len(res.Days) {
				res.Days[i].Spend[a.Repo] += dCost
			}
		}
		if !inRange || !keep(sid) {
			continue
		}
		a := get(sid)
		if !a.seen || r.TS < a.First {
			a.First = r.TS
		}
		a.seen = true
		if r.TS > a.Last {
			a.Last = r.TS
		}
		if r.HasCtx && (!a.ctxSeen || r.Ctx > *a.PeakCtx) {
			v := r.Ctx
			a.PeakCtx, a.ctxSeen = &v, true
		}
		switch r.Kind {
		case "status", "prompt", "tool_pre":
			b := r.TS / bucketMs
			if active[b] == nil {
				active[b] = map[string]bool{}
			}
			active[b][sid] = true
		}
		// waiting on you: from a permission request or notification to the next sign of life
		switch r.Kind {
		case "permission", "notification":
			if _, ok := waitFrom[sid]; !ok {
				waitFrom[sid] = r.TS
			}
		case "prompt", "tool_pre", "tool_post", "tool_fail":
			if t0, ok := waitFrom[sid]; ok {
				w := r.TS - t0
				res.Waits = append(res.Waits, float64(w)/1000)
				a.WaitMs += min(w, maxSpanMs)
				delete(waitFrom, sid)
			}
		case "session_end":
			delete(waitFrom, sid)
		}
		switch r.Kind {
		case "tool_pre":
			t := tools[r.Tool]
			if t == nil {
				t = &Tool{Name: r.Tool}
				tools[r.Tool] = t
			}
			t.Calls++
			res.KPI.ToolCalls++
			res.Heat[weekday(local(r.TS))][hour(local(r.TS))]++
			cmd := ""
			if r.Tool == "Bash" {
				cmd = CommandKey(r.Command)
			}
			if r.ToolUseID != "" {
				open[r.ToolUseID] = pending{ts: r.TS, tool: r.Tool, cmd: cmd}
			}
			if r.File != "" {
				f := relPath(r.File, in.Meta[sid].Cwd)
				fa := files[f]
				if fa == nil {
					fa = &fileAcc{agents: map[string]bool{}}
					files[f] = fa
				}
				if editTools[r.Tool] {
					fa.edits++
				} else {
					fa.reads++
				}
				fa.agents[sid] = true
			}
		case "tool_post", "tool_fail":
			p, ok := open[r.ToolUseID]
			if !ok {
				if r.Kind == "tool_fail" && tools[r.Tool] != nil {
					tools[r.Tool].Fails++
					res.KPI.ToolFails++
				}
				continue
			}
			delete(open, r.ToolUseID)
			d := r.TS - p.ts
			a.ToolMs += min(d, maxSpanMs)
			if p.cmd != "" {
				cmdRuns[p.cmd] = append(cmdRuns[p.cmd], d)
			}
			if r.Kind == "tool_fail" {
				if t := tools[p.tool]; t != nil {
					t.Fails++
				}
				res.KPI.ToolFails++
				if p.cmd != "" {
					cmdFails[p.cmd]++
				}
			}
		}
	}

	// repos (unfiltered), days already filled
	for repo, v := range repoSpend {
		res.Repos = append(res.Repos, RepoTotal{Repo: repo, Spend: v})
	}
	sort.Slice(res.Repos, func(i, j int) bool {
		if res.Repos[i].Spend != res.Repos[j].Spend {
			return res.Repos[i].Spend > res.Repos[j].Spend
		}
		return res.Repos[i].Repo < res.Repos[j].Repo
	})

	sort.Float64s(res.Waits)
	if n := len(res.Waits); n > 0 {
		res.KPI.WaitMedian = quantile(res.Waits, 0.5)
		res.KPI.WaitP90 = quantile(res.Waits, 0.9)
	}
	for _, s := range active {
		res.KPI.AgentHours += float64(len(s)) * float64(bucketMs) / float64(time.Hour/time.Millisecond)
		res.KPI.PeakAgents = max(res.KPI.PeakAgents, len(s))
	}

	for _, t := range tools {
		res.Tools = append(res.Tools, *t)
	}
	sort.Slice(res.Tools, func(i, j int) bool {
		if res.Tools[i].Calls != res.Tools[j].Calls {
			return res.Tools[i].Calls > res.Tools[j].Calls
		}
		return res.Tools[i].Name < res.Tools[j].Name
	})
	res.Tools = res.Tools[:min(len(res.Tools), topN)]

	for c, runs := range cmdRuns {
		sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
		fr := make([]float64, len(runs))
		for i, v := range runs {
			fr[i] = float64(v)
		}
		res.Commands = append(res.Commands, Command{Cmd: c, Runs: len(runs), MedianMs: int64(quantile(fr, 0.5)), P95Ms: int64(quantile(fr, 0.95)), Fails: cmdFails[c]})
	}
	sort.Slice(res.Commands, func(i, j int) bool {
		if res.Commands[i].MedianMs != res.Commands[j].MedianMs {
			return res.Commands[i].MedianMs > res.Commands[j].MedianMs
		}
		return res.Commands[i].Cmd < res.Commands[j].Cmd
	})
	res.Commands = res.Commands[:min(len(res.Commands), topN)]

	for p, f := range files {
		res.Files = append(res.Files, File{Path: p, Reads: f.reads, Edits: f.edits, Agents: len(f.agents)})
	}
	sort.Slice(res.Files, func(i, j int) bool {
		a, b := res.Files[i], res.Files[j]
		if a.Reads+a.Edits != b.Reads+b.Edits {
			return a.Reads+a.Edits > b.Reads+b.Edits
		}
		return a.Path < b.Path
	})
	res.Files = res.Files[:min(len(res.Files), topN)]

	for _, a := range sess {
		if a.seen {
			res.Sessions = append(res.Sessions, a.Session)
		}
	}
	sort.Slice(res.Sessions, func(i, j int) bool {
		if res.Sessions[i].Spend != res.Sessions[j].Spend {
			return res.Sessions[i].Spend > res.Sessions[j].Spend
		}
		return res.Sessions[i].Last > res.Sessions[j].Last
	})
	return res
}

// grow is how much a cumulative counter rose from prev to cur. A drop means a new claude
// process (a resume restarts the counters), so everything it shows is new.
func grow(prev, cur float64) float64 {
	if cur >= prev {
		return cur - prev
	}
	return cur
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := q * float64(len(sorted)-1)
	lo := int(math.Floor(i))
	hi := min(lo+1, len(sorted)-1)
	return sorted[lo] + (sorted[hi]-sorted[lo])*(i-float64(lo))
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func weekday(localMs int64) int { return (int(time.UnixMilli(localMs).UTC().Weekday()) + 6) % 7 }
func hour(localMs int64) int    { return time.UnixMilli(localMs).UTC().Hour() }

// modelFamily groups "Opus 5.5" / "claude-opus-5-5" style names by family for the token split.
func modelFamily(m string) string {
	l := strings.ToLower(m)
	for _, f := range []string{"opus", "sonnet", "haiku", "fable"} {
		if strings.Contains(l, f) {
			return strings.ToUpper(f[:1]) + f[1:]
		}
	}
	if m == "" {
		return "unknown"
	}
	return m
}

func relPath(file, cwd string) string {
	f := derive.NormPath(file)
	if cwd != "" {
		c := strings.TrimSuffix(derive.NormPath(cwd), "/") + "/"
		if strings.HasPrefix(f, c) {
			return strings.TrimPrefix(f, c)
		}
	}
	return f
}
