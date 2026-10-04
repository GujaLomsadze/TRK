package fleet

import (
	"testing"

	"github.com/GujaLomsadze/trk/internal/derive"
	"github.com/GujaLomsadze/trk/internal/model"
)

func TestViewSortCollisionsAndHiding(t *testing.T) {
	f := newFleet()
	now := 100000 * sec
	// W: working; P: waiting; D: done long ago (hidden)
	ingest(t, f, hook("W", "PreToolUse", "/r/w", `,"tool_name":"Edit","tool_use_id":"e1","tool_input":{"file_path":"/r/shared.go"}`), now-sec)
	ingest(t, f, hook("P", "PreToolUse", "/r/p", `,"tool_name":"Read","tool_use_id":"r1","tool_input":{"file_path":"/r/shared.go"}`), now-2*sec)
	ingest(t, f, hook("P", "PermissionRequest", "/r/p", `,"tool_name":"Bash","tool_input":{"command":"rm -rf build"}`), now-sec)
	old := now - 13*3600*sec
	ingest(t, f, model.Envelope{Source: "hook", TS: old, Payload: []byte(`{"session_id":"D","hook_event_name":"Stop","cwd":"/r/d"}`)}, old)

	v := f.View(now)
	if len(v.Sessions) != 2 || v.Sessions[0].SessionID != "P" || v.Sessions[1].SessionID != "W" {
		t.Fatalf("order = %v", ids(v.Sessions))
	}
	if len(v.Collisions) != 1 || v.Collisions[0].Path != "/r/shared.go" || len(v.Collisions[0].Names) != 2 {
		t.Fatalf("collisions = %+v", v.Collisions)
	}
	if v.Stats.Agents != 2 || v.Stats.Working != 1 || v.Stats.NeedsYou != 1 {
		t.Fatalf("stats = %+v", v.Stats)
	}
	var cats []string
	for _, b := range v.Sessions[0].Timeline {
		cats = append(cats, b.Cat)
	}
	if len(cats) != 2 || cats[0] != "read" || cats[1] != "wait" {
		t.Fatalf("P timeline cats = %v", cats)
	}
	if v.Sessions[0].Status != derive.StatusWaiting {
		t.Fatal("P not waiting")
	}
}

func TestDuplicateNamesDisambiguated(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	ingest(t, f, hook("aaaa1111", "SessionStart", "/x/api", ""), now)
	ingest(t, f, hook("bbbb2222", "SessionStart", "/y/api", ""), now)
	v := f.View(now)
	if v.Sessions[0].Name == v.Sessions[1].Name {
		t.Fatalf("names not disambiguated: %q", v.Sessions[0].Name)
	}
}

func ids(ss []SessionView) []string {
	var out []string
	for _, s := range ss {
		out = append(out, s.SessionID)
	}
	return out
}

// Cards keep a fixed slot: order is by session start, never by activity or status.
func TestViewOrderIsStable(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	ingest(t, f, hook("first", "SessionStart", "/r/a", ""), now)
	ingest(t, f, hook("second", "SessionStart", "/r/b", ""), now+sec)
	ingest(t, f, hook("third", "SessionStart", "/r/c", ""), now+2*sec)
	want := []string{"first", "second", "third"}
	check := func(when string) {
		t.Helper()
		got := ids(f.View(now + 10*sec).Sessions)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: order = %v, want %v", when, got, want)
			}
		}
	}
	check("initial")
	ingest(t, f, hook("third", "PreToolUse", "/r/c", `,"tool_name":"Bash","tool_input":{"command":"ls"}`), now+5*sec)
	check("after newest activity")
	ingest(t, f, hook("second", "PermissionRequest", "/r/b", `,"tool_name":"Bash","tool_input":{"command":"rm"}`), now+6*sec)
	check("after status change to waiting")
}

func TestViewCountsFilesAndCalls(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	for i, in := range []string{
		`,"tool_name":"Read","tool_use_id":"a","tool_input":{"file_path":"/r/a/x.go"}`,
		`,"tool_name":"Read","tool_use_id":"b","tool_input":{"file_path":"/r/a/z.go"}`,
		`,"tool_name":"Edit","tool_use_id":"c","tool_input":{"file_path":"/r/a/x.go"}`,
		`,"tool_name":"Write","tool_use_id":"d","tool_input":{"file_path":"/r/a/y.go"}`,
		`,"tool_name":"Bash","tool_use_id":"e","tool_input":{"command":"ls"}`,
	} {
		e := hook("A", "PreToolUse", "/r/a", in)
		e.TS = now + int64(i)*sec
		ingest(t, f, e, now+int64(i)*sec)
	}
	s := f.View(now + 10*sec).Sessions[0]
	if s.ToolCalls != 5 || s.FilesRead != 1 || s.FilesEdited != 2 {
		t.Fatalf("calls=%d read=%d edited=%d", s.ToolCalls, s.FilesRead, s.FilesEdited)
	}
}

func TestAutoHideIdleAndDone(t *testing.T) {
	f := newFleet()
	now := 100 * 3600 * sec
	ingest(t, f, hook("old", "PreToolUse", "/r/a", `,"tool_name":"Bash","tool_input":{"command":"ls"}`), now-6*3600*sec)   // idle 6h
	ingest(t, f, hook("fresh", "PreToolUse", "/r/b", `,"tool_name":"Bash","tool_input":{"command":"ls"}`), now-1*3600*sec) // idle 1h
	f.SetHideAfter(5 * 3600 * sec)
	if got := ids(f.View(now).Sessions); len(got) != 1 || got[0] != "fresh" {
		t.Fatalf("5h: %v", got)
	}
	f.SetHideAfter(0) // never
	if got := ids(f.View(now).Sessions); len(got) != 2 {
		t.Fatalf("never: %v", got)
	}
	if f.View(now).HideAfterMin != 0 {
		t.Fatal("view should report the setting")
	}
}

func TestDismissAndReturnOnActivity(t *testing.T) {
	f := newFleet()
	now := 1000 * sec
	ingest(t, f, hook("A", "SessionStart", "/r/a", ""), now)
	ingest(t, f, hook("A", "Stop", "/r/a", ""), now+sec) // done
	ingest(t, f, hook("W", "PreToolUse", "/r/w", `,"tool_name":"Bash","tool_input":{"command":"ls"}`), now+sec)
	if err := f.Dismiss("W", now+2*sec); err != ErrActive {
		t.Fatalf("dismissing a working session: %v", err)
	}
	if err := f.Dismiss("nope", now+2*sec); err != ErrNotFound {
		t.Fatalf("unknown session: %v", err)
	}
	if err := f.Dismiss("A", now+2*sec); err != nil {
		t.Fatal(err)
	}
	if got := ids(f.View(now + 3*sec).Sessions); len(got) != 1 || got[0] != "W" {
		t.Fatalf("after dismiss: %v", got)
	}
	// status-line refreshes are not activity: stays hidden
	ingest(t, f, model.Envelope{Source: "statusline", Payload: []byte(`{"session_id":"A","context_window":{"used_percentage":12}}`)}, now+4*sec)
	if got := ids(f.View(now + 5*sec).Sessions); len(got) != 1 {
		t.Fatalf("status refresh revived it: %v", got)
	}
	// a new prompt is: it comes back
	ingest(t, f, hook("A", "UserPromptSubmit", "/r/a", `,"user_prompt":"more"`), now+6*sec)
	if got := ids(f.View(now + 7*sec).Sessions); len(got) != 2 {
		t.Fatalf("new activity should bring it back: %v", got)
	}
}
