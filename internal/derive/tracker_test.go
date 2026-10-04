package derive

import "testing"

func TestTrackerOrdering(t *testing.T) {
	t.Run("late permission after its resolution is ignored", func(t *testing.T) {
		var tr Tracker
		tr.ClearPending(100) // PostToolUse arrived first
		tr.SetPending(Need{Type: "permission", Text: "Bash: rm", TS: 90})
		if tr.Pending != nil {
			t.Fatal("stale permission became pending")
		}
	})
	t.Run("stale clear does not drop newer permission", func(t *testing.T) {
		var tr Tracker
		tr.SetPending(Need{Type: "permission", TS: 90})
		tr.ClearPending(80)
		if tr.Pending == nil {
			t.Fatal("pending cleared by older event")
		}
		tr.ClearPending(95)
		if tr.Pending != nil || len(tr.Waits) != 1 || tr.Waits[0] != (Span{90, 95}) {
			t.Fatalf("pending=%v waits=%v", tr.Pending, tr.Waits)
		}
	})
	t.Run("strong need replaces weak text", func(t *testing.T) {
		var tr Tracker
		tr.SetPending(Need{Type: "permission", Text: "Claude needs your permission to use Bash", TS: 10, Weak: true})
		tr.SetPending(Need{Type: "permission", Text: "Bash: npm test", TS: 11})
		if tr.Pending.Text != "Bash: npm test" || tr.Pending.TS != 10 {
			t.Fatalf("pending = %+v", tr.Pending)
		}
	})
	t.Run("post before pre yields one finished call", func(t *testing.T) {
		var tr Tracker
		tr.ToolEnd(ToolCall{ID: "t1", Tool: "Bash", End: 200})
		tr.ToolStart(ToolCall{ID: "t1", Tool: "Bash", Start: 100})
		if len(tr.Calls) != 1 || tr.Calls[0].End != 200 {
			t.Fatalf("calls = %+v", tr.Calls)
		}
	})
	t.Run("pre then post", func(t *testing.T) {
		var tr Tracker
		tr.ToolStart(ToolCall{ID: "a", Start: 100})
		tr.ToolStart(ToolCall{ID: "b", Start: 50})
		tr.ToolEnd(ToolCall{ID: "a", End: 150, Failed: true})
		if len(tr.Calls) != 2 || tr.Calls[0].ID != "b" || !tr.Calls[1].Failed || tr.Calls[1].End != 150 {
			t.Fatalf("calls = %+v", tr.Calls)
		}
	})
	t.Run("prune", func(t *testing.T) {
		var tr Tracker
		tr.ToolStart(ToolCall{ID: "old", Start: 0, End: 1})
		tr.ToolStart(ToolCall{ID: "new", Start: KeepWindow + 10})
		tr.Waits = []Span{{0, 1}, {5, 0}}
		tr.Prune(KeepWindow + 20)
		if len(tr.Calls) != 1 || tr.Calls[0].ID != "new" || len(tr.Waits) != 1 {
			t.Fatalf("calls=%+v waits=%+v", tr.Calls, tr.Waits)
		}
	})
}
