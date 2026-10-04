package store

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
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
