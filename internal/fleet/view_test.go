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
