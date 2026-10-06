package stats

import (
	"testing"
	"time"

	"github.com/GujaLomsadze/trk/internal/store"
)

func TestCommandKey(t *testing.T) {
	cases := map[string]string{
		`make check`: "make check",
		`cd /mnt/d/REPOS/TRK; export PATH="$HOME/.local/go/bin:$PATH"; make check 2>&1 | tail -3`: "make check",
		`trk step "Writing tests"; go test ./...`:                                                 "go test",
		`S=/tmp/x; cd $S && node walk.js`:                                                         "node",
		`git push origin main 2>&1 | tail -1 && git tag v1`:                                       "git push",
		`gh run watch 123 -R GujaLomsadze/TRK --exit-status`:                                      "gh run",
		`go test -race -count=1 ./internal/...`:                                                   "go test",
		`sudo FOO=1 /usr/bin/python3 -c "print('a; b')"`:                                          "python3",
		`curl -s localhost:7777/healthz`:                                                          "curl",
		`(TRK_PORT=1 nohup ./trk serve > log 2>&1 &); sleep 1; curl x`:                            "trk",
		`R=$(gh run list --limit 1); gh run watch $R`:                                             "gh run",
		`cd /tmp`: "cd",
		``:        "(empty)",
	}
	for in, want := range cases {
		if got := CommandKey(in); got != want {
			t.Errorf("CommandKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCompute(t *testing.T) {
	loc := time.FixedZone("CEST", 2*3600)
	day0 := time.Date(2026, 10, 5, 0, 0, 0, 0, loc).UnixMilli() // Monday local midnight
	at := func(d int, hh, mm int) int64 { return day0 + int64(d)*day + int64(hh)*3600000 + int64(mm)*60000 }
	st := func(sid string, ts int64, cost, api, tok, ctx float64) store.StatRow {
		return store.StatRow{TS: ts, Session: sid, Kind: "status", Cost: cost, APIMs: api, Tokens: tok, Ctx: ctx, HasStatus: true, HasCtx: true}
	}
	ev := func(sid string, ts int64, kind, tool, id, cmd, file string) store.StatRow {
		return store.StatRow{TS: ts, Session: sid, Kind: kind, Tool: tool, ToolUseID: id, Command: cmd, File: file}
	}
	rows := []store.StatRow{
		st("A", at(-1, 10, 0), 4, 0, 0, 10), // previous period: 4 - prior 1 = 3
		st("A", at(0, 9, 0), 5, 1000, 100, 20),
		ev("A", at(0, 9, 1), "tool_pre", "Bash", "t1", "cd /r && make check", ""),
		ev("A", at(0, 9, 2), "tool_post", "Bash", "t1", "", ""),
		ev("A", at(0, 9, 3), "permission", "", "", "", ""),
		ev("A", at(0, 9, 8), "tool_pre", "Edit", "t2", "", "/r/web/app.js"), // waited 5 min
		ev("A", at(0, 9, 9), "tool_post", "Edit", "t2", "", ""),
		ev("A", at(0, 9, 10), "tool_pre", "Bash", "t3", "make check", ""),
		ev("A", at(0, 9, 13), "tool_fail", "Bash", "t3", "", ""),
		st("A", at(1, 10, 0), 2, 500, 50, 70), // counters restarted (resume): +2
		st("B", at(1, 22, 0), 7, 0, 0, 30),    // no prior: all 7 is new
		ev("B", at(1, 22, 1), "tool_pre", "Read", "t4", "", "/b/web/app.js"),
		ev("B", at(1, 22, 1), "tool_post", "Read", "t4", "", ""),
	}
	meta := map[string]Meta{"A": {Name: "A", Repo: "TRK", Model: "Opus 5.5", Cwd: "/r"}, "B": {Name: "B", Repo: "", Cwd: "/b", Model: "Sonnet 5.5"}}
	in := Input{Rows: rows, Prior: map[string]store.StatRow{"A": st("A", at(-2, 0, 0), 1, 0, 0, 0)}, Meta: meta,
		PrevSince: day0 - 2*day, Since: day0, Until: day0 + 2*day, Days: 2, TZOffsetMin: -120}
	r := Compute(in)
	if r.KPI.SpendPrev != 3 || r.KPI.Spend != 1+2+7 {
		t.Fatalf("spend %v prev %v", r.KPI.Spend, r.KPI.SpendPrev)
	}
	if r.Days[0].Spend["TRK"] != 1 || r.Days[1].Spend["TRK"] != 2 || r.Days[1].Spend["b"] != 7 {
		t.Fatalf("days %+v", r.Days)
	}
	if len(r.Repos) != 2 || r.Repos[0].Repo != "b" {
		t.Fatalf("repos %+v", r.Repos)
	}
	if len(r.Waits) != 1 || r.Waits[0] != 300 || r.KPI.WaitMedian != 300 {
		t.Fatalf("waits %v", r.Waits)
	}
	if r.Heat[0][9] != 3 || r.Heat[1][22] != 1 {
		t.Fatalf("heat mon9=%d tue22=%d", r.Heat[0][9], r.Heat[1][22])
	}
	if len(r.Commands) != 1 || r.Commands[0].Cmd != "make check" || r.Commands[0].Runs != 2 || r.Commands[0].Fails != 1 {
		t.Fatalf("commands %+v", r.Commands)
	}
	if r.KPI.ToolCalls != 4 || r.KPI.ToolFails != 1 || r.Tools[0].Name != "Bash" || r.Tools[0].Fails != 1 {
		t.Fatalf("tools %+v kpi %+v", r.Tools, r.KPI)
	}
	if len(r.Files) != 1 || r.Files[0].Path != "web/app.js" || r.Files[0].Reads != 1 || r.Files[0].Edits != 1 || r.Files[0].Agents != 2 {
		t.Fatalf("files %+v", r.Files)
	}
	a := r.Sessions[1]
	if r.Sessions[0].ID != "B" || a.ID != "A" || a.WaitMs != 300000 || a.ToolMs != (1+1+3)*60000 || a.ModelMs != 1000+500 || *a.PeakCtx != 70 {
		t.Fatalf("sessions %+v / %+v", r.Sessions[0], a)
	}
	if r.KPI.ByModel["Opus"] != 150 || r.KPI.PeakAgents != 1 {
		t.Fatalf("kpi %+v", r.KPI)
	}
	// repo filter: only TRK
	in.Repos = map[string]bool{"TRK": true}
	f := Compute(in)
	if f.KPI.Spend != 3 || len(f.Sessions) != 1 || len(f.Repos) != 2 {
		t.Fatalf("filtered: spend %v sessions %d repos %d", f.KPI.Spend, len(f.Sessions), len(f.Repos))
	}
}
