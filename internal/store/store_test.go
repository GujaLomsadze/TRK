package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GujaLomsadze/trk/internal/model"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "trk.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, p
}

func TestAppendAndQuery(t *testing.T) {
	s, _ := open(t)
	for i, k := range []string{"start", "step", "tool_pre"} {
		e := model.Event{TS: int64(1000 + i), SessionID: "a", Source: "cli", Kind: k, Payload: json.RawMessage(`{"x":1}`), Attribution: "env"}
		if err := s.Append(&e); err != nil {
			t.Fatal(err)
		}
		if e.ID == 0 {
			t.Fatal("ID not set")
		}
	}
	got, err := s.EventsSince(1001)
	if err != nil || len(got) != 2 || got[0].Kind != "step" || string(got[1].Payload) != `{"x":1}` {
		t.Fatalf("EventsSince = %+v, %v", got, err)
	}
	last, err := s.SessionEvents("a", 2)
	if err != nil || len(last) != 2 || last[0].Kind != "step" || last[1].Kind != "tool_pre" {
		t.Fatalf("SessionEvents = %+v, %v", last, err)
	}
}

func TestSessionUpsertNullableCtx(t *testing.T) {
	s, p := open(t)
	pct := 42.5
	in := model.Session{SessionID: "a", Name: "trk", Task: "x", StepI: 1, StepN: 5, CtxPct: &pct, CostUSD: 0.5, StartedAt: 1, LastEventAt: 2}
	if err := s.UpsertSession(in); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSession(model.Session{SessionID: "b"}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(p) // reopen: migrations must be idempotent
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	all, err := s2.Sessions()
	if err != nil || len(all) != 2 {
		t.Fatalf("Sessions = %+v, %v", all, err)
	}
	for _, ss := range all {
		switch ss.SessionID {
		case "a":
			if ss.CtxPct == nil || *ss.CtxPct != 42.5 || ss.StepN != 5 {
				t.Fatalf("a = %+v", ss)
			}
		case "b":
			if ss.CtxPct != nil {
				t.Fatalf("b ctx should be nil, got %v", *ss.CtxPct)
			}
		}
	}
}

func TestAccount(t *testing.T) {
	s, _ := open(t)
	a, err := s.Account()
	if err != nil || a.FiveHPct != nil {
		t.Fatalf("empty account = %+v, %v", a, err)
	}
	v := 23.0
	if err := s.SaveAccount(model.Account{FiveHPct: &v, FiveHReset: 99, UpdatedAt: 5}); err != nil {
		t.Fatal(err)
	}
	a, _ = s.Account()
	if a.FiveHPct == nil || *a.FiveHPct != 23 || a.SevenDPct != nil || a.FiveHReset != 99 {
		t.Fatalf("account = %+v", a)
	}
}

func TestLimitHistory(t *testing.T) {
	s, _ := open(t)
	add := func(ts int64, src, payload string) {
		t.Helper()
		e := model.Event{TS: ts, Source: src, Kind: "status", Payload: json.RawMessage(payload)}
		if err := s.Append(&e); err != nil {
			t.Fatal(err)
		}
	}
	rl := func(five, seven float64) string {
		b, _ := json.Marshal(map[string]any{"rate_limits": map[string]any{
			"five_hour": map[string]any{"used_percentage": five, "resets_at": 9000},
			"seven_day": map[string]any{"used_percentage": seven, "resets_at": 99000}}})
		return string(b)
	}
	add(500, "statusline", rl(1, 1))   // before since: dropped
	add(1000, "statusline", rl(10, 3)) // bucket 1000
	add(1500, "statusline", rl(12, 3)) // same bucket: highest wins
	add(1600, "hook", rl(90, 90))      // not a statusline event
	add(2100, "statusline", `{"x":1}`) // no rate limits
	add(3200, "statusline", rl(20, 4)) // bucket 3000

	got, err := s.LimitHistory("five_hour", 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	want := []LimitPoint{{TS: 1500, Pct: 12}, {TS: 3200, Pct: 20}}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("five_hour = %+v, want %+v", got, want)
	}
	got, _ = s.LimitHistory("seven_day", 0, 1000)
	if len(got) != 3 || got[2].Pct != 4 {
		t.Fatalf("seven_day = %+v", got)
	}
	if _, err := s.LimitHistory("bogus", 0, 1000); err == nil {
		t.Fatal("unknown window should error")
	}
}

func TestDismissedAtAndSettingsPersist(t *testing.T) {
	s, p := open(t)
	if err := s.UpsertSession(model.Session{SessionID: "a", DismissedAt: 42}); err != nil {
		t.Fatal(err)
	}
	if v, ok, err := s.Setting("hide_after_ms"); err != nil || ok || v != "" {
		t.Fatalf("unset setting = %q %v %v", v, ok, err)
	}
	if err := s.SetSetting("hide_after_ms", "18000000"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	all, _ := s2.Sessions()
	if len(all) != 1 || all[0].DismissedAt != 42 {
		t.Fatalf("sessions = %+v", all)
	}
	if v, ok, _ := s2.Setting("hide_after_ms"); !ok || v != "18000000" {
		t.Fatalf("setting = %q %v", v, ok)
	}
}

// A database created by v0.1.x (schema 1) must upgrade in place, keeping its rows.
func TestUpgradeFromSchema1(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	db.Exec(`PRAGMA user_version = 1`)
	db.Exec(`INSERT INTO sessions(session_id, name) VALUES('old', 'kept')`)
	db.Close()
	s, err := Open(p)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer s.Close()
	all, err := s.Sessions()
	if err != nil || len(all) != 1 || all[0].Name != "kept" || all[0].DismissedAt != 0 {
		t.Fatalf("after upgrade: %+v %v", all, err)
	}
}

func TestTrimToSizeDropsOldestEventsOnly(t *testing.T) {
	s, _ := open(t)
	big := json.RawMessage(`{"blob":"` + strings.Repeat("x", 4000) + `"}`)
	for i := 0; i < 300; i++ { // ~1.2 MB of payload
		if err := s.Append(&model.Event{TS: int64(i), SessionID: "a", Source: "hook", Kind: "tool_post", Payload: big}); err != nil {
			t.Fatal(err)
		}
	}
	s.UpsertSession(model.Session{SessionID: "a", Name: "keep me"})
	before, _ := s.UsedBytes()
	const max = 600 << 10
	if before <= max {
		t.Fatalf("test setup too small: %d", before)
	}
	deleted, err := s.TrimToSize(max)
	if err != nil || deleted == 0 {
		t.Fatalf("deleted=%d err=%v", deleted, err)
	}
	after, _ := s.UsedBytes()
	if after > max {
		t.Fatalf("still %d bytes used, cap %d", after, max)
	}
	left, _ := s.EventsSince(0)
	if len(left) == 0 || left[len(left)-1].TS != 299 || left[0].TS == 0 {
		t.Fatalf("expected newest kept and oldest gone, got %d events from ts %d", len(left), left[0].TS)
	}
	if ss, _ := s.Sessions(); len(ss) != 1 || ss[0].Name != "keep me" {
		t.Fatal("session rows must survive trimming")
	}
	if n, _ := s.TrimToSize(max); n != 0 {
		t.Fatalf("second trim deleted %d (already under cap)", n)
	}
}
