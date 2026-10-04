package fleet

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/GujaLomsadze/trk/internal/derive"
	"github.com/GujaLomsadze/trk/internal/gitinfo"
	"github.com/GujaLomsadze/trk/internal/model"
)

const sec = int64(1000)

func newFleet() *Fleet {
	return New(func(cwd string) gitinfo.Info {
		if strings.HasPrefix(cwd, "/r/") {
			return gitinfo.Info{Repo: "r", Branch: "main"}
		}
		return gitinfo.Info{}
	})
}

// ingest runs the daemon's pipeline minus storage.
func ingest(t *testing.T, f *Fleet, env model.Envelope, now int64) (model.Event, bool) {
	t.Helper()
	if env.TS == 0 {
		env.TS = now
	}
	ev, ok := f.Normalize(env, now)
	if ok {
		f.Apply(ev, now)
	}
	return ev, ok
}

func hook(sid, event, cwd string, extra string) model.Envelope {
	p := fmt.Sprintf(`{"session_id":%q,"hook_event_name":%q,"cwd":%q%s}`, sid, event, cwd, extra)
	return model.Envelope{Source: "hook", Payload: json.RawMessage(p)}
}

func cli(kind, payload string, pid int) model.Envelope {
	return model.Envelope{Source: "cli", Kind: kind, ClaudePID: pid, Payload: json.RawMessage(payload)}
}

func TestHookSessionLifecycle(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	ev, _ := ingest(t, f, hook("A", "SessionStart", "/r/a", `,"model":"claude-opus-5-5"`), now)
	if ev.Kind != model.KindSessionStart || ev.Attribution != model.AttrNative || ev.SessionID != "A" {
		t.Fatalf("event = %+v", ev)
	}
	ingest(t, f, hook("A", "PreToolUse", "/r/a", `,"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"npm test"}`), now+sec)
	ingest(t, f, hook("A", "PermissionRequest", "/r/a", `,"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"npm test"}`), now+2*sec)
	v := f.View(now + 3*sec)
	s := v.Sessions[0]
	if s.Status != derive.StatusWaiting || s.Name != "a" || s.Repo != "r" || s.Branch != "main" || s.Model != "claude-opus-5-5" {
		t.Fatalf("session = %+v", s.Session)
	}
	if len(v.Needs) != 1 || v.Needs[0].Text != "Bash: npm test" || !s.Attention {
		t.Fatalf("needs = %+v", v.Needs)
	}
	ingest(t, f, hook("A", "PostToolUse", "/r/a", `,"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"npm test"}`), now+4*sec)
	v = f.View(now + 5*sec)
	if v.Sessions[0].Status != derive.StatusWorking || len(v.Needs) != 0 {
		t.Fatalf("after post: %s needs=%v", v.Sessions[0].Status, v.Needs)
	}
	if r := v.Sessions[0].Recent; len(r) != 1 || r[0].State != "ok" || r[0].Summary != "npm test" {
		t.Fatalf("recent = %+v", r)
	}
	ingest(t, f, hook("A", "Stop", "/r/a", ""), now+6*sec)
	if st := f.View(now + 7*sec).Sessions[0].Status; st != derive.StatusDone {
		t.Fatalf("after stop: %s", st)
	}
}

func TestAttribution(t *testing.T) {
	cases := []struct {
		name     string
		env      model.Envelope
		wantSID  string
		wantAttr string
	}{
		{"env wins over pid", model.Envelope{Source: "cli", Kind: "step", SessionID: "B", SessionVia: "env", ClaudePID: 111, Payload: json.RawMessage(`{"text":"x","cwd":"/r/a"}`)}, "B", model.AttrEnv},
		{"third-party explicit session", model.Envelope{Source: "api", Kind: "step", SessionID: "ext", Payload: json.RawMessage(`{"text":"x"}`)}, "ext", model.AttrNative},
		{"pid learned from hooks", cli("step", `{"text":"x","cwd":"/somewhere"}`, 111), "A", model.AttrPtree},
		{"unknown pid falls back to cwd", cli("step", `{"text":"x","cwd":"/r/b"}`, 999), "B", model.AttrCwd},
		{"subdir cwd matches parent session", cli("step", `{"text":"x","cwd":"/r/b/sub/dir"}`, 0), "B", model.AttrCwd},
		{"exact beats prefix", cli("step", `{"text":"x","cwd":"/r/a/nested"}`, 0), "N", model.AttrCwd},
		{"no match → synthetic", cli("step", `{"text":"x","cwd":"/elsewhere"}`, 0), "", model.AttrCwd},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFleet()
			now := 1000 * sec
			a := hook("A", "SessionStart", "/r/a", "")
			a.ClaudePID = 111
			ingest(t, f, a, now)
			ingest(t, f, hook("B", "SessionStart", "/r/b", ""), now)
			ingest(t, f, hook("N", "SessionStart", "/r/a/nested", ""), now)
			ev, _ := ingest(t, f, c.env, now+sec)
			if c.wantSID == "" {
				if !strings.HasPrefix(ev.SessionID, "cwd-") {
					t.Fatalf("sid = %q, want synthetic", ev.SessionID)
				}
			} else if ev.SessionID != c.wantSID {
				t.Fatalf("sid = %q, want %q", ev.SessionID, c.wantSID)
			}
			if ev.Attribution != c.wantAttr {
				t.Fatalf("attr = %q, want %q", ev.Attribution, c.wantAttr)
			}
		})
	}
}

func TestCwdFallbackSkipsDoneSessions(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	ingest(t, f, hook("OLD", "SessionStart", "/r/a", ""), now)
	ingest(t, f, hook("OLD", "Stop", "/r/a", ""), now+sec)
	ev, _ := ingest(t, f, cli("step", `{"text":"x","cwd":"/r/a"}`, 0), now+2*sec)
	if ev.SessionID == "OLD" {
		t.Fatal("attributed to a done session")
	}
}

func TestDeclaredFlow(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	ingest(t, f, hook("A", "SessionStart", "/r/a", ""), now)
	env := func(kind, p string) model.Envelope {
		return model.Envelope{Source: "cli", Kind: kind, SessionID: "A", SessionVia: "env", Payload: json.RawMessage(p)}
	}
	ingest(t, f, env("start", `{"text":"Refactor consumer","steps":5}`), now+sec)
	ingest(t, f, env("step", `{"text":"Writing tests"}`), now+2*sec)
	ingest(t, f, env("progress", `{"i":3,"n":5}`), now+3*sec)
	s := f.View(now + 4*sec).Sessions[0]
	if s.Task != "Refactor consumer" || s.StepText != "Writing tests" || s.StepI != 3 || s.StepN != 5 {
		t.Fatalf("session = %+v", s.Session)
	}
	ingest(t, f, env("blocked", `{"text":"Drop legacy topic?"}`), now+5*sec)
	v := f.View(now + 6*sec)
	if v.Sessions[0].Status != derive.StatusBlocked || len(v.Needs) != 1 || v.Needs[0].Type != "blocked" {
		t.Fatalf("blocked view = %+v / %+v", v.Sessions[0].Status, v.Needs)
	}
	ingest(t, f, hook("A", "UserPromptSubmit", "/r/a", `,"user_prompt":"keep it"`), now+7*sec)
	if st := f.View(now + 8*sec).Sessions[0].Status; st != derive.StatusWorking {
		t.Fatalf("prompt should clear blocked, got %s", st)
	}
	ingest(t, f, env("done", `{"text":"Tests green"}`), now+9*sec)
	s = f.View(now + 10*sec).Sessions[0]
	if s.Status != derive.StatusDone || s.StepI != 5 || s.StepText != "Tests green" {
		t.Fatalf("done = %+v", s.Session)
	}
}

func TestApplyStatusNulls(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	full := `{"session_id":"A","cwd":"/r/a","model":{"display_name":"Opus"},"cost":{"total_cost_usd":1.5},
	  "context_window":{"used_percentage":55,"context_window_size":200000,"total_input_tokens":110000,"total_output_tokens":2000},
	  "rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1738425600},"seven_day":{"used_percentage":41.2,"resets_at":1738857600}}}`
	ingest(t, f, model.Envelope{Source: "statusline", Payload: json.RawMessage(full)}, now)
	s := f.View(now).Sessions[0]
	if s.CtxPct == nil || *s.CtxPct != 55 || s.CtxSize != 200000 || s.CostUSD != 1.5 || s.Model != "Opus" {
		t.Fatalf("session = %+v", s.Session)
	}
	if s.Ctx.Level != "yellow" || len(s.Ctx.Hints) != 1 {
		t.Fatalf("ctx band = %+v", s.Ctx)
	}
	a := f.Account()
	if a.FiveHPct == nil || *a.FiveHPct != 23.5 || a.FiveHReset != 1738425600000 || a.SevenDPct == nil {
		t.Fatalf("account = %+v", a)
	}
	nulls := `{"session_id":"A","context_window":{"used_percentage":null},"cost":{"total_cost_usd":"weird"}}`
	ingest(t, f, model.Envelope{Source: "statusline", Payload: json.RawMessage(nulls)}, now+sec)
	s = f.View(now + sec).Sessions[0]
	if s.CtxPct == nil || *s.CtxPct != 55 {
		t.Fatal("null used_percentage wiped the last known value")
	}
	if a := f.Account(); a.FiveHPct == nil {
		t.Fatal("absent rate_limits wiped the account snapshot")
	}
	if _, ok := f.Normalize(model.Envelope{Source: "statusline", TS: now + 2*sec, Payload: json.RawMessage(nulls)}, now+2*sec); ok {
		t.Fatal("duplicate status not dropped")
	}
}

func TestNormalizeTrimsHugePayload(t *testing.T) {
	f := newFleet()
	big := strings.Repeat("x", 5<<20)
	p := `{"session_id":"A","hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/r/a/x","content":"` + big + `"}}`
	ev, ok := f.Normalize(model.Envelope{Source: "hook", Payload: json.RawMessage(p)}, 1000*sec)
	if !ok || len(ev.Payload) > 16<<10 || !json.Valid(ev.Payload) || ev.Kind != model.KindToolPre {
		t.Fatalf("ok=%v len=%d kind=%s", ok, len(ev.Payload), ev.Kind)
	}
}

func TestNormalizeInvalidJSON(t *testing.T) {
	f := newFleet()
	ev, ok := f.Normalize(model.Envelope{Source: "hook", Payload: json.RawMessage(`{nope`)}, 1000*sec)
	if !ok || string(ev.Payload) != "{}" || ev.Kind != model.KindHookOther {
		t.Fatalf("ev = %+v", ev)
	}
	f.Apply(ev, 1000*sec) // must not panic
}

func TestNormalizeClampsClientClock(t *testing.T) {
	f := newFleet()
	now := 100000 * sec
	ev, _ := f.Normalize(model.Envelope{Source: "cli", Kind: "step", TS: now - 3600*sec, Payload: json.RawMessage(`{"cwd":"/x"}`)}, now)
	if ev.TS != now {
		t.Fatalf("ts = %d, want server now", ev.TS)
	}
}

func TestApplyOutOfOrderTool(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	post := hook("A", "PostToolUse", "/r/a", `,"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"ls"}`)
	post.TS = now + 2*sec
	pre := hook("A", "PreToolUse", "/r/a", `,"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"ls"}`)
	pre.TS = now + sec
	perm := hook("A", "PermissionRequest", "/r/a", `,"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":"ls"}`)
	perm.TS = now + 1500
	ingest(t, f, post, now+3*sec)
	ingest(t, f, pre, now+3*sec)
	ingest(t, f, perm, now+3*sec)
	sv, _ := f.SessionView("A", now+3*sec)
	if len(sv.Calls) != 1 || sv.Calls[0].End != now+2*sec || sv.Status == derive.StatusWaiting {
		t.Fatalf("calls=%+v status=%s", sv.Calls, sv.Status)
	}
}

func TestLoopReality(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	for i := 0; i < 5; i++ {
		e := hook("A", "PostToolUseFailure", "/r/a", fmt.Sprintf(`,"tool_name":"Bash","tool_use_id":"f%d","tool_input":{"command":"pytest -x"}`, i))
		e.TS = now + int64(i)*10*sec
		ingest(t, f, e, now+int64(i)*10*sec)
	}
	s := f.View(now + 50*sec).Sessions[0]
	if s.Status != derive.StatusLooping || !strings.Contains(s.Reality, "failed 5×") || !s.Attention {
		t.Fatalf("status=%s reality=%q", s.Status, s.Reality)
	}
}

func TestStatusDedupeIgnoresVolatileFields(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	mk := func(ms int) model.Envelope {
		p := fmt.Sprintf(`{"session_id":"A","cost":{"total_cost_usd":1.5,"total_duration_ms":%d},"context_window":{"used_percentage":40}}`, ms)
		return model.Envelope{Source: "statusline", Payload: json.RawMessage(p)}
	}
	if _, ok := ingest(t, f, mk(1000), now); !ok {
		t.Fatal("first status dropped")
	}
	if _, ok := ingest(t, f, mk(11000), now+10*sec); ok {
		t.Fatal("status differing only in total_duration_ms was stored again")
	}
	changed := model.Envelope{Source: "statusline", Payload: json.RawMessage(`{"session_id":"A","cost":{"total_cost_usd":1.6},"context_window":{"used_percentage":41}}`)}
	if _, ok := ingest(t, f, changed, now+20*sec); !ok {
		t.Fatal("real change dropped")
	}
}

// A daemon restart must not lose which Claude process belongs to which session,
// or CLI calls fall back to cwd matching and land on another session in the same folder.
func TestPIDMapSurvivesRestart(t *testing.T) {
	learned := map[int]string{}
	f := newFleet()
	f.OnLearn = func(pid int, sid string) { learned[pid] = sid }
	now := 1000 * sec
	a := hook("A", "SessionStart", "/r/shared", "")
	a.ClaudePID = 111
	ingest(t, f, a, now)
	again := hook("A", "PreToolUse", "/r/shared", `,"tool_name":"Bash","tool_input":{"command":"ls"}`)
	again.ClaudePID = 111
	calls := 0
	f.OnLearn = func(pid int, sid string) { calls++; learned[pid] = sid }
	ingest(t, f, again, now+sec)
	if calls != 0 {
		t.Fatalf("OnLearn called %d times for an unchanged mapping", calls)
	}
	if learned[111] != "A" {
		t.Fatalf("learned = %v", learned)
	}

	// restart: fresh fleet, sessions restored, pid map restored
	g := newFleet()
	g.Restore([]model.Session{{SessionID: "A", Cwd: "/r/shared", LastEventAt: now}, {SessionID: "B", Cwd: "/r/shared", LastEventAt: now + 5*sec}}, model.Account{})
	g.RestorePIDs(learned)
	ev, _ := ingest(t, g, cli("done", `{"text":"x","cwd":"/r/shared"}`, 111), now+10*sec)
	if ev.SessionID != "A" || ev.Attribution != model.AttrPtree {
		t.Fatalf("after restart: %s via %s, want A via ptree", ev.SessionID, ev.Attribution)
	}
}
