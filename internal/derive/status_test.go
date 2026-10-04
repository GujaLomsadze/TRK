package derive

import (
	"strings"
	"testing"
)

const min = 60 * 1000

func fails(n int, start, gap int64, key string) []ToolCall {
	var out []ToolCall
	for i := 0; i < n; i++ {
		ts := start + int64(i)*gap
		out = append(out, ToolCall{Tool: "Bash", Summary: "pytest -x", Key: key, Start: ts, End: ts + 1000, Failed: true})
	}
	return out
}

func edits(n int, start, gap int64, file string) []ToolCall {
	var out []ToolCall
	for i := 0; i < n; i++ {
		ts := start + int64(i)*gap
		out = append(out, ToolCall{Tool: "Edit", Summary: "x.go", Key: "Edit\x00" + string(rune('a'+i)), File: file, Edit: true, Start: ts, End: ts + 100})
	}
	return out
}

func TestStatus(t *testing.T) {
	now := int64(100 * min)
	cases := []struct {
		name string
		tr   Tracker
		want string
	}{
		{"fresh activity", Tracker{LastActivity: now - 1000}, StatusWorking},
		{"pending permission wins over everything", Tracker{LastActivity: now - 10*min, DoneAt: now, Pending: &Need{TS: now}, Blocked: &Need{}}, StatusWaiting},
		{"blocked", Tracker{LastActivity: now - 1000, Blocked: &Need{Type: "blocked"}}, StatusBlocked},
		{"stop with no later activity", Tracker{LastActivity: now - 5000, DoneAt: now - 4000}, StatusDone},
		{"activity after stop", Tracker{LastActivity: now - 1000, DoneAt: now - 4000}, StatusWorking},
		{"quiet 3 min", Tracker{LastActivity: now - 3*min}, StatusIdle},
		{"quiet 1m59s", Tracker{LastActivity: now - 2*min + 1000}, StatusWorking},
		{"5 identical failures in 10 min", Tracker{LastActivity: now - 1000, Calls: fails(5, now-8*min, min, "k")}, StatusLooping},
		{"4 failures", Tracker{LastActivity: now - 1000, Calls: fails(4, now-8*min, min, "k")}, StatusWorking},
		{"5 failures spread over 20 min", Tracker{LastActivity: now - 1000, Calls: fails(5, now-20*min, 4*min, "k")}, StatusWorking},
		{"done beats looping", Tracker{LastActivity: now - 2000, DoneAt: now - 1000, Calls: fails(6, now-8*min, min, "k")}, StatusDone},
		{"8 edits same file no progress", Tracker{LastActivity: now - 1000, Calls: edits(8, now-9*min, min, "/r/x.go")}, StatusLooping},
		{"8 edits but progress reported midway", Tracker{LastActivity: now - 1000, ProgressAt: now - 5*min, Calls: edits(8, now-9*min, min, "/r/x.go")}, StatusWorking},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := c.tr
			if got, _ := Status(&tr, now); got != c.want {
				t.Fatalf("Status = %s, want %s", got, c.want)
			}
		})
	}
}

func TestDetectLoopPicksWorstAndDescribes(t *testing.T) {
	now := int64(100 * min)
	calls := append(fails(5, now-9*min, min, "a"), fails(7, now-7*min, 30*1000, "b")...)
	l := DetectLoop(calls, 0, now)
	if l == nil || l.Count != 7 || l.Kind != "fail" {
		t.Fatalf("loop = %+v", l)
	}
	if !strings.Contains(l.Desc, "Bash `pytest -x` failed 7×") {
		t.Fatalf("desc = %q", l.Desc)
	}
	if DetectLoop(nil, 0, now) != nil {
		t.Fatal("loop on empty")
	}
}
