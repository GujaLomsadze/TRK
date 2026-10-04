# TRK M1 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship M1 of TRK: one static Go binary that is CLI, Claude Code hook/status-line handler and localhost daemon at once, with a phosphor-themed live dashboard showing every agent session.

**Architecture:** `trk <verb>` builds a small JSON envelope and POSTs it to the daemon (200 ms budget, always exit 0, auto-spawns `trk serve` if nothing answers). The daemon appends every event to SQLite (append-only), folds it into an in-memory `fleet` (sessions + per-session tracker), derives status/loops/collisions/context advice with pure functions, and pushes a full JSON snapshot over SSE (coalesced to ≤4/s) to a vanilla-JS dashboard embedded with `go:embed`.

**Tech Stack:** Go 1.27 (installed at `~/.local/go`), `modernc.org/sqlite` (pure Go, no CGO), `golang.org/x/sys` (process tree on macOS/Windows), stdlib `net/http` (1.22+ routing patterns), SSE, vanilla HTML/CSS/JS, IBM Plex Mono + VT323 (OFL, embedded), GoReleaser v2.

**Spec:** `TRK_SPEC.md` (repo root). Executors read both.

## Global Constraints

- One static binary, `CGO_ENABLED=0`. Targets: linux/darwin/windows × amd64/arm64. Binary target ~10–15 MB.
- Module path: `github.com/GujaLomsadze/trk`.
- Daemon binds `127.0.0.1` only. Default port `7777`. Env: `TRK_PORT`, `TRK_URL` (full override; disables auto-spawn), `TRK_DATA_DIR`, `TRK_SESSION`.
- Data dir defaults: Linux `~/.local/share/trk/` (honour `XDG_DATA_HOME`), macOS `~/Library/Application Support/trk/`, Windows `%APPDATA%\trk\`.
- Reporting verbs, `hook`, `statusline`: ~200 ms HTTP timeout, **always exit 0**, no stderr when the daemon is down. `hook` prints **nothing** to stdout (SessionStart/UserPromptSubmit stdout is injected into Claude's context).
- Auto-spawn budget: the one call that spawns the daemon may wait up to 1 s total for it; every other call stays within 200 ms.
- Storage: append-only `events`; `sessions` and `account` are rebuildable materializations. WAL mode. Index `events(session_id, ts)`.
- All timestamps internally are **unix ms**. Claude's `rate_limits.*.resets_at` is unix **seconds** → convert ×1000.
- Treat every field from Claude Code as optional; missing renders `—`.
- Hook events registered by `trk init` (verified against code.claude.com/docs/en/hooks on 2026-10-04): `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `Notification`, `Stop`, `SubagentStop`, `PreCompact`. Each registered as `{"type":"command","command":"<trk> hook","async":true,"timeout":10}` (async: TRK can never slow a tool call).
- Status rules (spec §6.3), precedence: waiting > blocked > done > idle > looping > working. Idle after 2 min without activity. Loop: same tool+normalized input failing ≥5× in 10 min, or same file edited ≥8× in 10 min with no progress report.
- **Context bands (user requirement, replaces spec's "warn ≥ 80%"):** <40% normal · ≥40% yellow · ≥60% red. Hints on the card: ≥50% "Start thinking about handing off / compacting"; ≥70% additionally "Don't trust architectural reasoning without re-grounding it".
- UI: phosphor tokens exactly as spec §7.2 table; glow only on title/chips/ASCII bars/secondary buttons; no glow on body text; agent-supplied text always inserted via `textContent` (never `innerHTML`).
- Commits: Conventional Commits. **No `Co-Authored-By` or any AI attribution.** Never `git push`, never open PRs — Guja does that. Work on branch `m1`.
- Tests: stdlib `testing` only, table-driven where the spec asks (status derivation, loop heuristic, settings.json merge, attribution fallback).
- Go commands below assume `export PATH="$HOME/.local/go/bin:$PATH"` (already in `~/.zshrc`).

## Review Focus

1. **Garbage on stdin to `trk hook` / `trk statusline`** (empty, non-JSON, 5 MB Write `tool_input.content`) → exit 0, hook prints nothing, statusline still prints a line, daemon stores a trimmed valid payload. Tests: Task 9 (`TestHookGarbageStdin`, `TestStatuslineGarbage`), Task 5 (`TestNormalizeTrimsHugePayload`, `TestNormalizeInvalidJSON`).
2. **Async hooks arrive out of order** (PostToolUse before PreToolUse; PermissionRequest after the PostToolUse that resolved it) → one tool call, no phantom "waiting". Tests: Task 4 (`TestTrackerOrdering`), Task 5 (`TestApplyOutOfOrderTool`).
3. **Hostile / odd `settings.json`** (missing, empty, invalid JSON, `hooks` not an object, symlinked dotfile, second run) → invalid input aborts with no write; second run is a no-op with no new backup; symlink target is edited, link preserved; key order preserved. Tests: Task 10.
4. **Browser attacks on a localhost daemon** (cross-origin POST from a web page, DNS-rebinding Host header, `<script>` in a task name) → 403 for foreign Host/Origin; dashboard renders text literally. Tests: Task 6 (`TestGuard`), Task 11 (`TestNoInnerHTML`).
5. **Status line fields null or absent** (`used_percentage: null` early in session, no `rate_limits` for API users, window dropped after `resets_at`) → `—`, account snapshot not wiped, no panic. Tests: Task 5 (`TestApplyStatusNulls`), Task 9 (`TestCompactLine`).

---

## File Structure

```
go.mod / go.sum
cmd/trk/main.go                     # os.Exit(cli.Run(...))
internal/version/version.go         # Version string, set by ldflags
internal/config/config.go           # port, base URL, data dir
internal/model/model.go             # Envelope, Event, Session, Account, kinds, hook map
internal/store/store.go             # SQLite open/migrate/append/query/upsert
internal/gitinfo/gitinfo.go         # repo + branch from cwd, no exec
internal/derive/tracker.go          # per-session Tracker: tool calls, pending need, ordering
internal/derive/describe.go         # tool summary, loop key, file, category
internal/derive/status.go           # Status(), DetectLoop()
internal/derive/collisions.go       # Collisions()
internal/derive/ctx.go              # CtxAdvice() — context bands + hints
internal/fleet/payload.go           # tolerant payload parsing + trimming
internal/fleet/fleet.go             # Normalize (attribution), Apply (materializer), Restore
internal/fleet/view.go              # View/SessionView/needs/timeline/sort
internal/daemon/server.go           # HTTP handlers, guard
internal/daemon/hub.go              # SSE fan-out
internal/daemon/run.go              # listen, restore, loop, pid file
internal/ptree/ptree.go (+ _linux/_darwin/_windows/_other)
internal/client/client.go (+ spawn_unix.go / spawn_windows.go)
internal/cli/cli.go                 # dispatch
internal/cli/report.go              # start/step/progress/blocked/done
internal/cli/integrations.go        # hook, statusline
internal/cli/serve.go               # serve, open
internal/cli/init.go                # init
internal/claudecfg/ojson.go         # order-preserving JSON
internal/claudecfg/merge.go         # hooks/statusLine merge, CLAUDE.md block
internal/claudecfg/init.go          # Init orchestration, backup, atomic write
web/embed.go, web/index.html, web/theme.css, web/app.js, web/fonts/*
e2e/e2e_test.go                     # build tag e2e
scripts/fetch-fonts.sh, scripts/seed-demo.sh
.goreleaser.yaml, install.sh, .github/workflows/{ci,release}.yml, README.md, .gitignore
```

---

### Task 1: Scaffold, config, version, CLI dispatch

**Files:**
- Create: `go.mod`, `.gitignore`, `cmd/trk/main.go`, `internal/version/version.go`, `internal/config/config.go`, `internal/cli/cli.go`
- Test: `internal/config/config_test.go`, `internal/cli/cli_test.go`

**Interfaces:**
- Produces: `config.Port() int`, `config.BaseURL() string`, `config.IsLocalDefault() bool`, `config.DataDir() (string, error)`, `version.Version string`, `cli.Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int`.

- [ ] **Step 1: Branch and init module**

```bash
cd /mnt/d/REPOS/TRK
git checkout -b m1
go mod init github.com/GujaLomsadze/trk
printf 'dist/\n/trk\n/trk.exe\n*.db\n*.db-wal\n*.db-shm\n' > .gitignore
```

- [ ] **Step 2: Write failing tests**

`internal/config/config_test.go`:
```go
package config

import "testing"

func TestBaseURL(t *testing.T) {
	cases := []struct {
		name, port, url, want string
		local                 bool
	}{
		{"default", "", "", "http://127.0.0.1:7777", true},
		{"port", "9000", "", "http://127.0.0.1:9000", true},
		{"bad port", "nope", "", "http://127.0.0.1:7777", true},
		{"out of range", "70000", "", "http://127.0.0.1:7777", true},
		{"url override", "9000", "http://host.docker.internal:7777/", "http://host.docker.internal:7777", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TRK_PORT", c.port)
			t.Setenv("TRK_URL", c.url)
			if got := BaseURL(); got != c.want {
				t.Fatalf("BaseURL() = %q, want %q", got, c.want)
			}
			if got := IsLocalDefault(); got != c.local {
				t.Fatalf("IsLocalDefault() = %v, want %v", got, c.local)
			}
		})
	}
}

func TestDataDirOverride(t *testing.T) {
	t.Setenv("TRK_DATA_DIR", "/tmp/trk-x")
	got, err := DataDir()
	if err != nil || got != "/tmp/trk-x" {
		t.Fatalf("DataDir() = %q, %v", got, err)
	}
}
```

`internal/cli/cli_test.go`:
```go
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"version"}, strings.NewReader(""), &out, &errb)
	if code != 0 || !strings.HasPrefix(out.String(), "trk ") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"frobnicate"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
}
```

- [ ] **Step 3: Run to see failure**

Run: `go test ./...` — Expected: FAIL, `undefined: BaseURL` / `undefined: Run`.

- [ ] **Step 4: Implement**

`internal/version/version.go`:
```go
// Package version holds the build version, injected by GoReleaser via -ldflags.
package version

var Version = "dev"
```

`internal/config/config.go`:
```go
// Package config resolves TRK's environment-driven settings. Everything is optional.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const DefaultPort = 7777

func Port() int {
	if p, err := strconv.Atoi(os.Getenv("TRK_PORT")); err == nil && p > 0 && p < 65536 {
		return p
	}
	return DefaultPort
}

// BaseURL is where clients send events. TRK_URL wins (e.g. WSL ↔ Windows).
func BaseURL() string {
	if u := strings.TrimRight(os.Getenv("TRK_URL"), "/"); u != "" {
		return u
	}
	return fmt.Sprintf("http://127.0.0.1:%d", Port())
}

// IsLocalDefault reports whether clients talk to a daemon this machine may spawn.
func IsLocalDefault() bool { return os.Getenv("TRK_URL") == "" }

func DataDir() (string, error) {
	if d := os.Getenv("TRK_DATA_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "trk"), nil
	case "windows":
		if a := os.Getenv("APPDATA"); a != "" {
			return filepath.Join(a, "trk"), nil
		}
	}
	if x := os.Getenv("XDG_DATA_HOME"); x != "" {
		return filepath.Join(x, "trk"), nil
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "trk"), nil
}
```

`internal/cli/cli.go`:
```go
// Package cli implements every trk subcommand.
package cli

import (
	"fmt"
	"io"

	"github.com/GujaLomsadze/trk/internal/version"
)

const usageText = `trk — live tracker for coding agents

  trk serve                      run daemon + dashboard (auto-spawned when needed)
  trk open                       open the dashboard
  trk init                       wire up Claude Code hooks, status line, CLAUDE.md

  trk start "<task>" --steps N   declare a task
  trk step "<what>"              declare the current step
  trk progress i/N               declare progress
  trk blocked "<question>"       ask the human for a decision
  trk done "<result>"            declare completion

  trk hook                       (Claude Code) forward hook JSON from stdin
  trk statusline [--then CMD]    (Claude Code) forward status JSON, print a line
  trk version
`

func usage(w io.Writer) { fmt.Fprint(w, usageText) }

// Run executes one trk invocation and returns the process exit code.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stdout)
		return 0
	}
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "trk %s\n", version.Version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "trk: unknown command %q (try `trk help`)\n", args[0])
	return 2
}
```

`cmd/trk/main.go`:
```go
package main

import (
	"os"

	"github.com/GujaLomsadze/trk/internal/cli"
)

func main() { os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) }
```

- [ ] **Step 5: Run tests** — `go test ./... && go build ./cmd/trk && ./trk version` → PASS, prints `trk dev`.

- [ ] **Step 6: Commit**

```bash
git add TRK_SPEC.md docs/plan.md go.mod .gitignore cmd internal
git commit -m "chore: scaffold trk module, config and CLI dispatch"
```

---

### Task 2: Model and SQLite store

**Files:**
- Create: `internal/model/model.go`, `internal/store/store.go`
- Test: `internal/model/model_test.go`, `internal/store/store_test.go`

**Interfaces:**
- Produces (model):
  - `type Envelope struct{ TS int64; Source, Kind, SessionID, SessionVia string; ClaudePID int; Payload json.RawMessage }` (json: `ts, source, kind, session_id, session_via, claude_pid, payload`)
  - `type Event struct{ ID, TS int64; SessionID, Source, Kind string; Payload json.RawMessage; Attribution string }`
  - `type Session struct{...}` (fields below), `type Account struct{...}`
  - Kind constants, `Source*` constants, `Attr*` constants, `HookEvents []string`, `KindForHook(string) string`, `IsActivity(string) bool`
- Produces (store): `Open(path string) (*Store, error)`, `(*Store) Close() error`, `Append(*model.Event) error`, `EventsSince(ms int64) ([]model.Event, error)`, `SessionEvents(id string, limit int) ([]model.Event, error)`, `UpsertSession(model.Session) error`, `Sessions() ([]model.Session, error)`, `SaveAccount(model.Account) error`, `Account() (model.Account, error)`.

- [ ] **Step 1: Add dependency** — `go get modernc.org/sqlite@latest`

- [ ] **Step 2: Write failing tests**

`internal/model/model_test.go`:
```go
package model

import "testing"

func TestKindForHook(t *testing.T) {
	cases := map[string]string{
		"PreToolUse": KindToolPre, "PostToolUseFailure": KindToolFail,
		"PermissionRequest": KindPermission, "Stop": KindStop, "Whatever": KindHookOther, "": KindHookOther,
	}
	for in, want := range cases {
		if got := KindForHook(in); got != want {
			t.Errorf("KindForHook(%q) = %q, want %q", in, got, want)
		}
	}
	for _, ev := range HookEvents {
		if KindForHook(ev) == KindHookOther {
			t.Errorf("registered hook %q has no kind", ev)
		}
	}
}
```

`internal/store/store_test.go`:
```go
package store

import (
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
```

- [ ] **Step 3: Run** — `go test ./internal/model ./internal/store` → FAIL (undefined).

- [ ] **Step 4: Implement**

`internal/model/model.go`:
```go
// Package model defines the wire envelope, stored events and materialized state.
package model

import "encoding/json"

// Envelope is what clients POST to /v1/events.
type Envelope struct {
	TS         int64           `json:"ts,omitempty"`          // unix ms at the caller
	Source     string          `json:"source"`                // cli|hook|statusline|api
	Kind       string          `json:"kind,omitempty"`        // empty for hook/statusline: derived by the daemon
	SessionID  string          `json:"session_id,omitempty"`  // explicit session (TRK_SESSION or third-party)
	SessionVia string          `json:"session_via,omitempty"` // "env" when SessionID came from TRK_SESSION
	ClaudePID  int             `json:"claude_pid,omitempty"`  // nearest ancestor claude process, 0 if none
	Payload    json.RawMessage `json:"payload,omitempty"`
}

type Event struct {
	ID          int64           `json:"id"`
	TS          int64           `json:"ts"`
	SessionID   string          `json:"session_id"`
	Source      string          `json:"source"`
	Kind        string          `json:"kind"`
	Payload     json.RawMessage `json:"payload"`
	Attribution string          `json:"attribution"`
}

type Session struct {
	SessionID   string   `json:"session_id"`
	Name        string   `json:"name"`
	Cwd         string   `json:"cwd"`
	Repo        string   `json:"repo"`
	Branch      string   `json:"branch"`
	Model       string   `json:"model"`
	Task        string   `json:"task"`
	StepText    string   `json:"step_text"`
	StepI       int      `json:"step_i"`
	StepN       int      `json:"step_n"`
	Status      string   `json:"status"`
	CtxPct      *float64 `json:"ctx_pct"`
	CtxSize     int64    `json:"ctx_size"`
	CostUSD     float64  `json:"cost_usd"`
	TokensIn    int64    `json:"tokens_in"`
	TokensOut   int64    `json:"tokens_out"`
	StartedAt   int64    `json:"started_at"`
	LastEventAt int64    `json:"last_event_at"`
}

// Account is the latest plan-limit snapshot. Resets are unix ms.
type Account struct {
	FiveHPct    *float64 `json:"five_h_pct"`
	FiveHReset  int64    `json:"five_h_reset"`
	SevenDPct   *float64 `json:"seven_d_pct"`
	SevenDReset int64    `json:"seven_d_reset"`
	UpdatedAt   int64    `json:"updated_at"`
}

const (
	SourceCLI        = "cli"
	SourceHook       = "hook"
	SourceStatusline = "statusline"
	SourceAPI        = "api"
)

const (
	AttrEnv    = "env"
	AttrPtree  = "ptree"
	AttrCwd    = "cwd"
	AttrNative = "native"
)

const (
	KindStart        = "start"
	KindStep         = "step"
	KindProgress     = "progress"
	KindBlocked      = "blocked"
	KindDone         = "done"
	KindSessionStart = "session_start"
	KindSessionEnd   = "session_end"
	KindPrompt       = "prompt"
	KindToolPre      = "tool_pre"
	KindToolPost     = "tool_post"
	KindToolFail     = "tool_fail"
	KindPermission   = "permission"
	KindNotification = "notification"
	KindStop         = "stop"
	KindSubagentStop = "subagent_stop"
	KindCompact      = "compact"
	KindStatus       = "status"
	KindHookOther    = "hook"
	KindOther        = "event"
)

// HookEvents are the Claude Code hook events trk init registers.
var HookEvents = []string{
	"SessionStart", "SessionEnd", "UserPromptSubmit", "PreToolUse", "PostToolUse",
	"PostToolUseFailure", "PermissionRequest", "Notification", "Stop", "SubagentStop", "PreCompact",
}

var hookKinds = map[string]string{
	"SessionStart":       KindSessionStart,
	"SessionEnd":         KindSessionEnd,
	"UserPromptSubmit":   KindPrompt,
	"PreToolUse":         KindToolPre,
	"PostToolUse":        KindToolPost,
	"PostToolUseFailure": KindToolFail,
	"PermissionRequest":  KindPermission,
	"Notification":       KindNotification,
	"Stop":               KindStop,
	"SubagentStop":       KindSubagentStop,
	"PreCompact":         KindCompact,
}

func KindForHook(event string) string {
	if k, ok := hookKinds[event]; ok {
		return k
	}
	return KindHookOther
}

// IsActivity reports whether an event kind means the agent is doing something
// (as opposed to bookkeeping: status updates, notifications, turn ends).
func IsActivity(kind string) bool {
	switch kind {
	case KindStart, KindStep, KindProgress, KindBlocked, KindDone, KindSessionStart, KindPrompt,
		KindToolPre, KindToolPost, KindToolFail, KindPermission, KindCompact, KindSubagentStop:
		return true
	}
	return false
}
```

`internal/store/store.go`:
```go
// Package store persists the append-only event log and materialized state in SQLite.
package store

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/GujaLomsadze/trk/internal/model"
	_ "modernc.org/sqlite"
)

var migrations = []string{`
CREATE TABLE events(
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,
  session_id TEXT,
  source TEXT NOT NULL,
  kind TEXT NOT NULL,
  payload TEXT NOT NULL,
  attribution TEXT
);
CREATE INDEX events_session_ts ON events(session_id, ts);
CREATE INDEX events_ts ON events(ts);
CREATE TABLE sessions(
  session_id TEXT PRIMARY KEY,
  name TEXT, cwd TEXT, repo TEXT, branch TEXT, model TEXT,
  task TEXT, step_text TEXT, step_i INTEGER, step_n INTEGER,
  status TEXT,
  ctx_pct REAL, ctx_size INTEGER, cost_usd REAL, tokens_in INTEGER, tokens_out INTEGER,
  started_at INTEGER, last_event_at INTEGER
);
CREATE TABLE account(
  id INTEGER PRIMARY KEY CHECK (id = 1),
  five_h_pct REAL, five_h_reset INTEGER, seven_d_pct REAL, seven_d_reset INTEGER, updated_at INTEGER
);`}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // single writer; SQLite serializes anyway
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, i+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Append(e *model.Event) error {
	res, err := s.db.Exec(`INSERT INTO events(ts, session_id, source, kind, payload, attribution) VALUES(?,?,?,?,?,?)`,
		e.TS, e.SessionID, e.Source, e.Kind, string(e.Payload), e.Attribution)
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

const eventCols = `id, ts, session_id, source, kind, payload, attribution`

func scanEvents(rows *sql.Rows) ([]model.Event, error) {
	defer rows.Close()
	var out []model.Event
	for rows.Next() {
		var e model.Event
		var sid, attr sql.NullString
		var payload string
		if err := rows.Scan(&e.ID, &e.TS, &sid, &e.Source, &e.Kind, &payload, &attr); err != nil {
			return nil, err
		}
		e.SessionID, e.Attribution, e.Payload = sid.String, attr.String, json.RawMessage(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// EventsSince returns events with ts >= ms in log order.
func (s *Store) EventsSince(ms int64) ([]model.Event, error) {
	rows, err := s.db.Query(`SELECT `+eventCols+` FROM events WHERE ts >= ? ORDER BY ts, id`, ms)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

// SessionEvents returns the session's most recent events, oldest first.
func (s *Store) SessionEvents(id string, limit int) ([]model.Event, error) {
	rows, err := s.db.Query(`SELECT * FROM (SELECT `+eventCols+` FROM events WHERE session_id = ? ORDER BY ts DESC, id DESC LIMIT ?) ORDER BY ts, id`, id, limit)
	if err != nil {
		return nil, err
	}
	return scanEvents(rows)
}

func (s *Store) UpsertSession(x model.Session) error {
	_, err := s.db.Exec(`INSERT INTO sessions(session_id, name, cwd, repo, branch, model, task, step_text, step_i, step_n, status,
  ctx_pct, ctx_size, cost_usd, tokens_in, tokens_out, started_at, last_event_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(session_id) DO UPDATE SET name=excluded.name, cwd=excluded.cwd, repo=excluded.repo, branch=excluded.branch,
  model=excluded.model, task=excluded.task, step_text=excluded.step_text, step_i=excluded.step_i, step_n=excluded.step_n,
  status=excluded.status, ctx_pct=excluded.ctx_pct, ctx_size=excluded.ctx_size, cost_usd=excluded.cost_usd,
  tokens_in=excluded.tokens_in, tokens_out=excluded.tokens_out, started_at=excluded.started_at, last_event_at=excluded.last_event_at`,
		x.SessionID, x.Name, x.Cwd, x.Repo, x.Branch, x.Model, x.Task, x.StepText, x.StepI, x.StepN, x.Status,
		x.CtxPct, x.CtxSize, x.CostUSD, x.TokensIn, x.TokensOut, x.StartedAt, x.LastEventAt)
	return err
}

func (s *Store) Sessions() ([]model.Session, error) {
	rows, err := s.db.Query(`SELECT session_id, coalesce(name,''), coalesce(cwd,''), coalesce(repo,''), coalesce(branch,''),
  coalesce(model,''), coalesce(task,''), coalesce(step_text,''), coalesce(step_i,0), coalesce(step_n,0), coalesce(status,''),
  ctx_pct, coalesce(ctx_size,0), coalesce(cost_usd,0), coalesce(tokens_in,0), coalesce(tokens_out,0),
  coalesce(started_at,0), coalesce(last_event_at,0) FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Session
	for rows.Next() {
		var x model.Session
		if err := rows.Scan(&x.SessionID, &x.Name, &x.Cwd, &x.Repo, &x.Branch, &x.Model, &x.Task, &x.StepText,
			&x.StepI, &x.StepN, &x.Status, &x.CtxPct, &x.CtxSize, &x.CostUSD, &x.TokensIn, &x.TokensOut,
			&x.StartedAt, &x.LastEventAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

func (s *Store) SaveAccount(a model.Account) error {
	_, err := s.db.Exec(`INSERT INTO account(id, five_h_pct, five_h_reset, seven_d_pct, seven_d_reset, updated_at) VALUES(1,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET five_h_pct=excluded.five_h_pct, five_h_reset=excluded.five_h_reset,
  seven_d_pct=excluded.seven_d_pct, seven_d_reset=excluded.seven_d_reset, updated_at=excluded.updated_at`,
		a.FiveHPct, a.FiveHReset, a.SevenDPct, a.SevenDReset, a.UpdatedAt)
	return err
}

func (s *Store) Account() (model.Account, error) {
	var a model.Account
	err := s.db.QueryRow(`SELECT five_h_pct, coalesce(five_h_reset,0), seven_d_pct, coalesce(seven_d_reset,0), coalesce(updated_at,0) FROM account WHERE id = 1`).
		Scan(&a.FiveHPct, &a.FiveHReset, &a.SevenDPct, &a.SevenDReset, &a.UpdatedAt)
	if err == sql.ErrNoRows {
		return model.Account{}, nil
	}
	return a, err
}
```

- [ ] **Step 5: Run** — `go test ./internal/model ./internal/store` → PASS. (If scanning into `**float64` fails, switch those columns to `sql.NullFloat64` and convert.)

- [ ] **Step 6: Commit** — `git add -A internal/model internal/store go.mod go.sum && git commit -m "feat(store): sqlite event log and materialized sessions"`

---

### Task 3: gitinfo (repo + branch without exec)

**Files:**
- Create: `internal/gitinfo/gitinfo.go`
- Test: `internal/gitinfo/gitinfo_test.go`

**Interfaces:**
- Produces: `type Info struct{ Repo, Branch string }`, `func Lookup(cwd string) Info`.

- [ ] **Step 1: Failing test**

```go
package gitinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLookup(t *testing.T) {
	root := t.TempDir()
	main := filepath.Join(root, "myrepo")
	write(t, filepath.Join(main, ".git", "HEAD"), "ref: refs/heads/feature/x\n")
	os.MkdirAll(filepath.Join(main, "sub", "deep"), 0o755)

	wt := filepath.Join(root, "myrepo-wt")
	wtGit := filepath.Join(main, ".git", "worktrees", "myrepo-wt")
	write(t, filepath.Join(wtGit, "HEAD"), "ref: refs/heads/wt-branch\n")
	write(t, filepath.Join(wtGit, "commondir"), "../..\n")
	write(t, filepath.Join(wt, ".git"), "gitdir: "+wtGit+"\n")

	det := filepath.Join(root, "detached")
	write(t, filepath.Join(det, ".git", "HEAD"), "0123456789abcdef0123456789abcdef01234567\n")

	cases := []struct {
		cwd  string
		want Info
	}{
		{main, Info{"myrepo", "feature/x"}},
		{filepath.Join(main, "sub", "deep"), Info{"myrepo", "feature/x"}},
		{wt, Info{"myrepo", "wt-branch"}},
		{det, Info{"detached", "0123456"}},
		{t.TempDir(), Info{}},
		{"", Info{}},
	}
	for _, c := range cases {
		if got := Lookup(c.cwd); got != c.want {
			t.Errorf("Lookup(%q) = %+v, want %+v", c.cwd, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/gitinfo` → FAIL.

- [ ] **Step 3: Implement**

```go
// Package gitinfo reads repo name and branch straight from .git files (no git exec, so it's cheap).
package gitinfo

import (
	"os"
	"path/filepath"
	"strings"
)

type Info struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

func Lookup(cwd string) Info {
	if cwd == "" {
		return Info{}
	}
	dir := filepath.Clean(cwd)
	for {
		dotgit := filepath.Join(dir, ".git")
		if st, err := os.Stat(dotgit); err == nil {
			gitDir, common := dotgit, dotgit
			if !st.IsDir() { // worktree or submodule: ".git" file points elsewhere
				b, err := os.ReadFile(dotgit)
				if err != nil {
					return Info{}
				}
				gd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
				if !filepath.IsAbs(gd) {
					gd = filepath.Join(dir, gd)
				}
				gitDir, common = gd, gd
				if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
					cd := strings.TrimSpace(string(c))
					if !filepath.IsAbs(cd) {
						cd = filepath.Join(gd, cd)
					}
					common = filepath.Clean(cd)
				}
			}
			return Info{Repo: repoName(common, dir), Branch: branch(gitDir)}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Info{}
		}
		dir = parent
	}
}

func repoName(common, workdir string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Base(filepath.Dir(common))
	}
	if strings.Contains(filepath.ToSlash(common), "/.git/") { // submodule: .git/modules/<name>
		return filepath.Base(workdir)
	}
	return strings.TrimSuffix(filepath.Base(common), ".git") // bare repo
}

func branch(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if len(head) >= 7 {
		return head[:7]
	}
	return head
}
```

- [ ] **Step 4: Run** — `go test ./internal/gitinfo` → PASS.
- [ ] **Step 5: Commit** — `git add internal/gitinfo && git commit -m "feat(gitinfo): repo and branch from .git without exec"`

---

### Task 4: derive — tracker, tool description, status, loops, collisions, context advice

Pure functions, no I/O. This is where the spec's heuristics live, so it carries the bulk of the table-driven tests.

**Files:**
- Create: `internal/derive/tracker.go`, `internal/derive/describe.go`, `internal/derive/status.go`, `internal/derive/collisions.go`, `internal/derive/ctx.go`
- Test: `internal/derive/tracker_test.go`, `internal/derive/describe_test.go`, `internal/derive/status_test.go`, `internal/derive/collisions_test.go`, `internal/derive/ctx_test.go`

**Interfaces:**
- Produces:
  - Constants `IdleAfter, LoopWindow, CollisionWindow, KeepWindow int64` (ms), `LoopFailures, ChurnEdits int`, `Status*` strings.
  - `type ToolCall struct{ ID, Tool, Summary, Key, File string; Edit bool; Start, End int64; Failed bool }`
  - `type Need struct{ Type, Text string; TS int64; Weak bool }`, `type Span struct{ Start, End int64 }`
  - `type Tracker struct{ ProgressAt int64; Blocked, Pending *Need; LastClear, DoneAt, LastActivity int64; Calls []ToolCall; Waits []Span }` with methods `Activity(ts)`, `SetPending(Need)`, `ClearPending(ts)`, `ToolStart(ToolCall)`, `ToolEnd(ToolCall)`, `Prune(now)`
  - `Describe(tool string, input json.RawMessage, cwd string) (summary, key, file string, edit bool)`, `Category(tool string) string` (`read|edit|run`)
  - `type Loop struct{ Kind, Tool, Summary, Desc string; Count int; FirstTS, LastTS int64 }`, `DetectLoop(calls []ToolCall, progressAt, now int64) *Loop`, `Status(t *Tracker, now int64) (string, *Loop)`
  - `type FileTouch struct{ Session, Path string; TS int64; Edit bool }`, `type Collision struct{ Path string; Sessions []string; LastTS int64 }`, `Collisions([]FileTouch, now int64) []Collision`
  - `type CtxBand struct{ Level string; Hints []string }`, `CtxAdvice(pct *float64) CtxBand`, constants `HintHandoff`, `HintReground`

- [ ] **Step 1: Failing tests**

`internal/derive/ctx_test.go`:
```go
package derive

import (
	"reflect"
	"testing"
)

func f(v float64) *float64 { return &v }

func TestCtxAdvice(t *testing.T) {
	cases := []struct {
		pct   *float64
		level string
		hints []string
	}{
		{nil, "none", nil},
		{f(0), "ok", nil},
		{f(39.9), "ok", nil},
		{f(40), "yellow", nil},
		{f(49.9), "yellow", nil},
		{f(50), "yellow", []string{HintHandoff}},
		{f(59.9), "yellow", []string{HintHandoff}},
		{f(60), "red", []string{HintHandoff}},
		{f(69.9), "red", []string{HintHandoff}},
		{f(70), "red", []string{HintHandoff, HintReground}},
		{f(98), "red", []string{HintHandoff, HintReground}},
	}
	for _, c := range cases {
		got := CtxAdvice(c.pct)
		if got.Level != c.level || !reflect.DeepEqual(got.Hints, c.hints) {
			t.Errorf("CtxAdvice(%v) = %+v, want %s %v", c.pct, got, c.level, c.hints)
		}
	}
}
```

`internal/derive/tracker_test.go`:
```go
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
```

`internal/derive/describe_test.go`:
```go
package derive

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDescribe(t *testing.T) {
	cases := []struct {
		tool, input, cwd string
		sum, file       string
		edit            bool
	}{
		{"Bash", `{"command":"go   test ./...\nsecond line"}`, "", "go   test ./...", "", false},
		{"Edit", `{"file_path":"/r/a/main.go","old_string":"x"}`, "/r/a", "main.go", "/r/a/main.go", true},
		{"Write", `{"file_path":"/elsewhere/x.txt","content":"..."}`, "/r/a", "/elsewhere/x.txt", "/elsewhere/x.txt", true},
		{"Read", `{"file_path":"/r/a/b/c.go"}`, "/r/a", "b/c.go", "/r/a/b/c.go", false},
		{"NotebookEdit", `{"notebook_path":"/r/a/n.ipynb"}`, "/r/a", "n.ipynb", "/r/a/n.ipynb", true},
		{"Grep", `{"pattern":"TODO"}`, "", "TODO", "", false},
		{"WebFetch", `{"url":"https://x.dev"}`, "", "https://x.dev", "", false},
		{"Task", `{"description":"find callers"}`, "", "find callers", "", false},
		{"mcp__x__y", `{"a":1}`, "", "", "", false},
		{"Bash", `not json`, "", "", "", false},
	}
	for _, c := range cases {
		sum, _, file, edit := Describe(c.tool, json.RawMessage(c.input), c.cwd)
		if sum != c.sum || file != c.file || edit != c.edit {
			t.Errorf("Describe(%s,%s) = %q,%q,%v want %q,%q,%v", c.tool, c.input, sum, file, edit, c.sum, c.file, c.edit)
		}
	}
	_, k1, _, _ := Describe("Bash", json.RawMessage(`{"command":"pytest  -x"}`), "")
	_, k2, _, _ := Describe("Bash", json.RawMessage(`{"command":" pytest -x "}`), "")
	if k1 != k2 || k1 == "" {
		t.Errorf("whitespace-normalized keys differ: %q vs %q", k1, k2)
	}
	_, k3, _, _ := Describe("Edit", json.RawMessage(`{"b":2,"a":1}`), "")
	_, k4, _, _ := Describe("Edit", json.RawMessage(`{"a":1,"b":2}`), "")
	if k3 != k4 {
		t.Errorf("key order changed key: %q vs %q", k3, k4)
	}
	long, _, _, _ := Describe("Bash", json.RawMessage(`{"command":"`+strings.Repeat("x", 300)+`"}`), "")
	if r := []rune(long); len(r) != 121 || !strings.HasSuffix(long, "…") {
		t.Errorf("summary not truncated: %d runes", len(r))
	}
}

func TestCategory(t *testing.T) {
	for tool, want := range map[string]string{"Read": "read", "Grep": "read", "Glob": "read", "WebFetch": "read",
		"Edit": "edit", "Write": "edit", "MultiEdit": "edit", "NotebookEdit": "edit", "Bash": "run", "Task": "run"} {
		if got := Category(tool); got != want {
			t.Errorf("Category(%s) = %s, want %s", tool, got, want)
		}
	}
}
```

`internal/derive/status_test.go`:
```go
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
```

`internal/derive/collisions_test.go`:
```go
package derive

import "testing"

func TestCollisions(t *testing.T) {
	now := int64(60 * min)
	cases := []struct {
		name    string
		touches []FileTouch
		want    int
	}{
		{"edit + read by two sessions", []FileTouch{{"a", "/r/x.go", now - min, true}, {"b", "/r/x.go", now - 2*min, false}}, 1},
		{"two edits", []FileTouch{{"a", "/r/x.go", now - min, true}, {"b", "/r/x.go", now - min, true}}, 1},
		{"reads only", []FileTouch{{"a", "/r/x.go", now - min, false}, {"b", "/r/x.go", now - min, false}}, 0},
		{"same session twice", []FileTouch{{"a", "/r/x.go", now - min, true}, {"a", "/r/x.go", now, true}}, 0},
		{"outside window", []FileTouch{{"a", "/r/x.go", now - 11*min, true}, {"b", "/r/x.go", now - min, true}}, 0},
		{"different files", []FileTouch{{"a", "/r/x.go", now, true}, {"b", "/r/y.go", now, true}}, 0},
		{"unclean paths match", []FileTouch{{"a", "/r/./x.go", now, true}, {"b", "/r/x.go", now, true}}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Collisions(c.touches, now)
			if len(got) != c.want {
				t.Fatalf("got %+v, want %d", got, c.want)
			}
			if c.want == 1 && (len(got[0].Sessions) != 2 || got[0].Sessions[0] != "a") {
				t.Fatalf("sessions = %v", got[0].Sessions)
			}
		})
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/derive` → FAIL (undefined).

- [ ] **Step 3: Implement**

`internal/derive/ctx.go`:
```go
package derive

// Context bands (user requirement): ≥40 yellow, ≥60 red; hints at 50 and 70.
const (
	CtxYellow   = 40.0
	CtxHandoff  = 50.0
	CtxRed      = 60.0
	CtxReground = 70.0

	HintHandoff  = "Start thinking about handing off / compacting"
	HintReground = "Don't trust architectural reasoning without re-grounding it"
)

type CtxBand struct {
	Level string   `json:"level"` // none|ok|yellow|red
	Hints []string `json:"hints,omitempty"`
}

func CtxAdvice(pct *float64) CtxBand {
	if pct == nil {
		return CtxBand{Level: "none"}
	}
	p := *pct
	b := CtxBand{Level: "ok"}
	if p >= CtxYellow {
		b.Level = "yellow"
	}
	if p >= CtxRed {
		b.Level = "red"
	}
	if p >= CtxHandoff {
		b.Hints = append(b.Hints, HintHandoff)
	}
	if p >= CtxReground {
		b.Hints = append(b.Hints, HintReground)
	}
	return b
}
```

`internal/derive/tracker.go`:
```go
// Package derive holds TRK's pure heuristics: status, loops, collisions, context advice.
package derive

import "sort"

const (
	IdleAfter       int64 = 2 * 60 * 1000
	LoopWindow      int64 = 10 * 60 * 1000
	CollisionWindow int64 = 10 * 60 * 1000
	KeepWindow      int64 = 30 * 60 * 1000 // tool calls kept for loop detection + timeline
	LoopFailures          = 5
	ChurnEdits            = 8
)

type ToolCall struct {
	ID      string `json:"-"` // tool_use_id
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Key     string `json:"-"` // tool + normalized input
	File    string `json:"file,omitempty"`
	Edit    bool   `json:"edit,omitempty"`
	Start   int64  `json:"start"`
	End     int64  `json:"end,omitempty"` // 0 while running
	Failed  bool   `json:"failed,omitempty"`
}

type Need struct {
	Type string `json:"type"` // permission|question|blocked
	Text string `json:"text"`
	TS   int64  `json:"ts"`
	Weak bool   `json:"-"` // from a Notification; a PermissionRequest has better text
}

type Span struct {
	Start int64 `json:"start"`
	End   int64 `json:"end"` // 0 while open
}

// Tracker is the per-session working memory the heuristics read.
// Hooks run async, so events can arrive out of order: every mutation compares timestamps.
type Tracker struct {
	ProgressAt   int64 // last start/step/progress
	Blocked      *Need // declared via trk blocked
	Pending      *Need // permission prompt / question awaiting the human
	LastClear    int64 // latest event that resolves Pending
	DoneAt       int64 // done/stop/session_end
	LastActivity int64
	Calls        []ToolCall
	Waits        []Span
}

func (t *Tracker) Activity(ts int64) {
	if ts > t.LastActivity {
		t.LastActivity = ts
	}
}

func (t *Tracker) SetPending(n Need) {
	if n.TS <= t.LastClear {
		return // already resolved by a later event that arrived first
	}
	if t.Pending != nil {
		if t.Pending.Weak && !n.Weak {
			t.Pending.Text, t.Pending.Type, t.Pending.Weak = n.Text, n.Type, false
		}
		return
	}
	t.Pending = &n
	t.Waits = append(t.Waits, Span{Start: n.TS})
}

func (t *Tracker) ClearPending(ts int64) {
	if ts > t.LastClear {
		t.LastClear = ts
	}
	if t.Pending == nil || ts < t.Pending.TS {
		return
	}
	if n := len(t.Waits); n > 0 && t.Waits[n-1].End == 0 {
		t.Waits[n-1].End = ts
	}
	t.Pending = nil
}

func (t *Tracker) find(id string) int {
	if id == "" {
		return -1
	}
	for i := range t.Calls {
		if t.Calls[i].ID == id {
			return i
		}
	}
	return -1
}

func (t *Tracker) insert(c ToolCall) {
	i := sort.Search(len(t.Calls), func(i int) bool { return t.Calls[i].Start > c.Start })
	t.Calls = append(t.Calls, ToolCall{})
	copy(t.Calls[i+1:], t.Calls[i:])
	t.Calls[i] = c
}

func (t *Tracker) ToolStart(c ToolCall) {
	if i := t.find(c.ID); i >= 0 {
		if c.Start < t.Calls[i].Start { // post arrived first and guessed Start=End
			t.Calls[i].Start = c.Start
			sort.SliceStable(t.Calls, func(a, b int) bool { return t.Calls[a].Start < t.Calls[b].Start })
		}
		return
	}
	t.insert(c)
}

func (t *Tracker) ToolEnd(c ToolCall) {
	if i := t.find(c.ID); i >= 0 {
		t.Calls[i].End, t.Calls[i].Failed = c.End, c.Failed
		return
	}
	if c.Start == 0 {
		c.Start = c.End
	}
	t.insert(c)
}

func (t *Tracker) Prune(now int64) {
	cut := now - KeepWindow
	i := 0
	for i < len(t.Calls) && t.Calls[i].Start < cut {
		i++
	}
	t.Calls = t.Calls[i:]
	w := t.Waits[:0]
	for _, s := range t.Waits {
		if s.End == 0 || s.End >= cut {
			w = append(w, s)
		}
	}
	t.Waits = w
}
```

`internal/derive/describe.go`:
```go
package derive

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

const maxSummary = 120

// Describe turns a tool call into a one-line summary, a loop-detection key,
// the file it touches (absolute, cleaned) and whether it edits.
func Describe(tool string, input json.RawMessage, cwd string) (summary, key, file string, edit bool) {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	str := func(k string) string { s, _ := in[k].(string); return s }

	switch tool {
	case "Bash":
		cmd := str("command")
		summary = firstLine(cmd)
		key = tool + "\x00" + strings.Join(strings.Fields(cmd), " ")
		if cmd == "" {
			key = ""
		}
		return trunc(summary), key, "", false
	case "Edit", "Write", "MultiEdit", "Read", "NotebookEdit", "NotebookRead":
		p := str("file_path")
		if p == "" {
			p = str("notebook_path")
		}
		if p != "" {
			file = filepath.Clean(p)
			summary = rel(file, cwd)
		}
		edit = tool == "Edit" || tool == "Write" || tool == "MultiEdit" || tool == "NotebookEdit"
	case "Grep", "Glob":
		summary = str("pattern")
	case "WebFetch":
		summary = str("url")
	case "WebSearch":
		summary = str("query")
	case "Task", "Agent":
		summary = str("description")
	}
	if in != nil {
		b, _ := json.Marshal(in) // map keys marshal sorted → order-independent key
		key = tool + "\x00" + string(b)
	}
	return trunc(firstLine(summary)), key, file, edit
}

func Category(tool string) string {
	switch tool {
	case "Read", "Grep", "Glob", "LS", "WebFetch", "WebSearch", "NotebookRead":
		return "read"
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "edit"
	}
	return "run"
}

func rel(path, cwd string) string {
	if cwd != "" {
		if r, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return filepath.ToSlash(path)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func trunc(s string) string {
	r := []rune(s)
	if len(r) <= maxSummary {
		return s
	}
	return string(r[:maxSummary]) + "…"
}
```

`internal/derive/status.go`:
```go
package derive

import (
	"fmt"
	"path/filepath"
	"sort"
)

const (
	StatusWorking = "working"
	StatusWaiting = "waiting"
	StatusBlocked = "blocked"
	StatusIdle    = "idle"
	StatusLooping = "looping"
	StatusDone    = "done"
)

type Loop struct {
	Kind    string `json:"kind"` // fail|churn
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
	Desc    string `json:"desc"`
	Count   int    `json:"count"`
	FirstTS int64  `json:"first_ts"`
	LastTS  int64  `json:"last_ts"`
}

// Status applies spec §6.3. Precedence: waiting > blocked > done > idle > looping > working.
// The loop is returned even when another status wins, so the UI can still show the reality box.
func Status(t *Tracker, now int64) (string, *Loop) {
	loop := DetectLoop(t.Calls, t.ProgressAt, now)
	switch {
	case t.Pending != nil:
		return StatusWaiting, loop
	case t.Blocked != nil:
		return StatusBlocked, loop
	case t.DoneAt > 0 && t.LastActivity <= t.DoneAt:
		return StatusDone, loop
	case now-t.LastActivity > IdleAfter:
		return StatusIdle, loop
	case loop != nil:
		return StatusLooping, loop
	}
	return StatusWorking, nil
}

// DetectLoop finds the worst repetition in the last LoopWindow: the same tool+input
// failing ≥ LoopFailures times, or one file edited ≥ ChurnEdits times since the
// last declared progress.
func DetectLoop(calls []ToolCall, progressAt, now int64) *Loop {
	since := now - LoopWindow
	fails := map[string]*Loop{}
	churn := map[string]*Loop{}
	bump := func(m map[string]*Loop, k, kind string, c ToolCall) {
		l := m[k]
		if l == nil {
			l = &Loop{Kind: kind, Tool: c.Tool, Summary: c.Summary, FirstTS: c.Start}
			m[k] = l
		}
		l.Count++
		end := c.End
		if end == 0 {
			end = c.Start
		}
		if end > l.LastTS {
			l.LastTS = end
		}
	}
	for _, c := range calls {
		if c.Start < since {
			continue
		}
		if c.Failed && c.Key != "" {
			bump(fails, c.Key, "fail", c)
		}
		if c.Edit && c.File != "" && c.Start > progressAt {
			bump(churn, c.File, "churn", c)
		}
	}
	if l := worst(fails, LoopFailures); l != nil {
		l.Desc = fmt.Sprintf("%s `%s` failed %d× in %s", l.Tool, l.Summary, l.Count, span(l.LastTS-l.FirstTS))
		return l
	}
	if l := worst(churn, ChurnEdits); l != nil {
		l.Desc = fmt.Sprintf("%s edited %d× in %s with no progress update", filepath.Base(l.Summary), l.Count, span(l.LastTS-l.FirstTS))
		return l
	}
	return nil
}

func worst(m map[string]*Loop, threshold int) *Loop {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys) // deterministic tie-break
	var best *Loop
	for _, k := range keys {
		if l := m[k]; l.Count >= threshold && (best == nil || l.Count > best.Count) {
			best = l
		}
	}
	return best
}

func span(ms int64) string {
	if ms < 60*1000 {
		return fmt.Sprintf("%ds", ms/1000)
	}
	return fmt.Sprintf("%d min", ms/60000)
}
```

`internal/derive/collisions.go`:
```go
package derive

import (
	"path/filepath"
	"sort"
)

type FileTouch struct {
	Session string
	Path    string
	TS      int64
	Edit    bool
}

type Collision struct {
	Path     string   `json:"path"`
	Sessions []string `json:"sessions"`
	LastTS   int64    `json:"last_ts"`
}

// Collisions: ≥2 distinct active sessions touched the same file within
// CollisionWindow and at least one of them edited it. Callers pass only active sessions.
func Collisions(touches []FileTouch, now int64) []Collision {
	type acc struct {
		sessions map[string]bool
		edited   bool
		last     int64
	}
	byPath := map[string]*acc{}
	for _, t := range touches {
		if t.Path == "" || t.TS < now-CollisionWindow {
			continue
		}
		p := filepath.Clean(t.Path)
		a := byPath[p]
		if a == nil {
			a = &acc{sessions: map[string]bool{}}
			byPath[p] = a
		}
		a.sessions[t.Session] = true
		a.edited = a.edited || t.Edit
		if t.TS > a.last {
			a.last = t.TS
		}
	}
	var out []Collision
	for p, a := range byPath {
		if len(a.sessions) < 2 || !a.edited {
			continue
		}
		c := Collision{Path: p, LastTS: a.last}
		for s := range a.sessions {
			c.Sessions = append(c.Sessions, s)
		}
		sort.Strings(c.Sessions)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastTS != out[j].LastTS {
			return out[i].LastTS > out[j].LastTS
		}
		return out[i].Path < out[j].Path
	})
	return out
}
```

- [ ] **Step 4: Run** — `go test ./internal/derive -v` → PASS.
- [ ] **Step 5: Commit** — `git add internal/derive && git commit -m "feat(derive): status, loop, collision and context-band heuristics"`

---

### Task 5: fleet — normalize (attribution), apply (materializer), view

**Files:**
- Create: `internal/fleet/payload.go`, `internal/fleet/fleet.go`, `internal/fleet/view.go`
- Test: `internal/fleet/fleet_test.go`, `internal/fleet/view_test.go`

**Interfaces:**
- Consumes: `model.*`, `derive.*`, `gitinfo.Info`.
- Produces:
  - `type GitLookup func(cwd string) gitinfo.Info`
  - `func New(git GitLookup) *Fleet`
  - `(*Fleet) Normalize(env model.Envelope, now int64) (model.Event, bool)` — false = drop (duplicate status)
  - `(*Fleet) Apply(ev model.Event, now int64) model.Session`
  - `(*Fleet) Restore(sessions []model.Session, acct model.Account)`
  - `(*Fleet) Account() model.Account`
  - `(*Fleet) View(now int64) View`, `(*Fleet) SessionView(id string, now int64) (SessionView, bool)`
  - `type View struct{ Now int64; Sessions []SessionView; Needs []NeedItem; Collisions []CollisionView; Account model.Account; Stats Stats }` (json: `now, sessions, needs, collisions, account, stats`)
  - `type SessionView struct{ model.Session; Loop *derive.Loop; Reality string; Recent []CallView; Timeline []Block; Attention bool; Need *derive.Need; Ctx derive.CtxBand; Calls []derive.ToolCall }` (`Calls` only filled by `SessionView()`, `omitempty`)
  - `type CallView struct{ Tool, Summary, State string }` (state `ok|fail|run`), `type Block struct{ Start, End int64; Cat string }` (cat `read|edit|run|wait`), `type NeedItem struct{ SessionID, Name string; derive.Need }`, `type CollisionView struct{ derive.Collision; Names []string }`, `type Stats struct{ Agents, Working, NeedsYou int; TokensToday int64 }`

Attribution order (spec §5.4): hook/statusline payload `session_id` → `native`; envelope `session_id` with `session_via=env` → `env` (without → `native`, third-party API); `claude_pid` known from earlier hooks → `ptree`; best cwd match among non-done sessions (exact > subdir, then same git branch, then most recent) → `cwd`; else synthetic `cwd-<sha1(cwd)[:8]>` (so non-Claude agents still get a card) → `cwd`.

Decisions baked in (spec is silent): a `prompt` (UserPromptSubmit) clears `blocked` (the human answered); `Stop` marks the turn done (spec §6.3); done/idle sessions older than 12 h drop off the view; status updates whose payload hash equals the previous one for that session are dropped (the user's status line has `refreshInterval: 10`); string values longer than 2048 runes inside payloads >16 KB are truncated before storage.

- [ ] **Step 1: Failing tests**

`internal/fleet/fleet_test.go`:
```go
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
```

`internal/fleet/view_test.go`:
```go
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
```

- [ ] **Step 2: Run** — `go test ./internal/fleet` → FAIL (undefined).

- [ ] **Step 3: Implement**

`internal/fleet/payload.go`:
```go
package fleet

import (
	"bytes"
	"encoding/json"
	"errors"
)

// payload is the union of fields TRK reads from CLI, hook and status line JSON.
// Everything is optional; unknown fields are ignored; type mismatches skip the field.
type payload struct {
	// CLI verbs
	Text  string `json:"text"`
	Steps int    `json:"steps"`
	I     int    `json:"i"`
	N     int    `json:"n"`
	// shared
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	Model     json.RawMessage `json:"model"` // string (SessionStart) or object (status line)
	// hooks
	HookEvent        string          `json:"hook_event_name"`
	ToolName         string          `json:"tool_name"`
	ToolInput        json.RawMessage `json:"tool_input"`
	ToolUseID        string          `json:"tool_use_id"`
	Message          string          `json:"message"`
	NotificationType string          `json:"notification_type"`
	// status line
	Workspace *struct {
		CurrentDir string `json:"current_dir"`
	} `json:"workspace"`
	Cost *struct {
		TotalCostUSD *float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow *struct {
		UsedPercentage    *float64 `json:"used_percentage"`
		ContextWindowSize *float64 `json:"context_window_size"`
		TotalInputTokens  *float64 `json:"total_input_tokens"`
		TotalOutputTokens *float64 `json:"total_output_tokens"`
	} `json:"context_window"`
	RateLimits *struct {
		FiveHour *window `json:"five_hour"`
		SevenDay *window `json:"seven_day"`
	} `json:"rate_limits"`
}

type window struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"` // unix seconds
}

func parse(raw []byte) payload {
	var p payload
	err := json.Unmarshal(raw, &p)
	var typeErr *json.UnmarshalTypeError
	if err != nil && !errors.As(err, &typeErr) {
		return payload{} // syntax error: nothing usable
	}
	return p // on a type error, encoding/json still fills every other field
}

func (p payload) cwd() string {
	if p.Cwd != "" {
		return p.Cwd
	}
	if p.Workspace != nil {
		return p.Workspace.CurrentDir
	}
	return ""
}

func (p payload) modelName() string {
	m := bytes.TrimSpace(p.Model)
	if len(m) == 0 {
		return ""
	}
	if m[0] == '"' {
		var s string
		_ = json.Unmarshal(m, &s)
		return s
	}
	var o struct {
		DisplayName string `json:"display_name"`
		ID          string `json:"id"`
	}
	_ = json.Unmarshal(m, &o)
	if o.DisplayName != "" {
		return o.DisplayName
	}
	return o.ID
}

const (
	trimAbove  = 16 << 10
	maxStrRune = 2048
)

// trimPayload keeps stored payloads small: Write contents and tool responses can be megabytes.
func trimPayload(raw json.RawMessage) json.RawMessage {
	if len(raw) <= trimAbove {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return json.RawMessage(`{}`)
	}
	limit := maxStrRune
	for {
		out, err := json.Marshal(shrink(v, limit))
		if err != nil {
			return json.RawMessage(`{}`)
		}
		if len(out) <= trimAbove || limit <= 64 {
			return out
		}
		limit /= 4 // many medium strings: tighten until it fits
	}
}

func shrink(v any, limit int) any {
	switch t := v.(type) {
	case string:
		if r := []rune(t); len(r) > limit {
			return string(r[:limit]) + "…[trimmed]"
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, x := range t {
			out[k] = shrink(x, limit)
		}
		return out
	case []any:
		if len(t) > 200 {
			t = t[:200]
		}
		out := make([]any, len(t))
		for i, x := range t {
			out[i] = shrink(x, limit)
		}
		return out
	}
	return v
}
```

`internal/fleet/fleet.go`:
```go
// Package fleet materializes the event stream into live session state.
package fleet

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"hash/fnv"
	"path/filepath"
	"strings"
	"sync"

	"github.com/GujaLomsadze/trk/internal/derive"
	"github.com/GujaLomsadze/trk/internal/gitinfo"
	"github.com/GujaLomsadze/trk/internal/model"
)

type GitLookup func(cwd string) gitinfo.Info

const (
	clockSkew   = 10 * 60 * 1000 // client ts further than this from server now → use server now
	gitCacheTTL = 30 * 1000
)

type entry struct {
	S          model.Session
	T          derive.Tracker
	statusHash uint64
}

type gitCached struct {
	info gitinfo.Info
	at   int64
}

type Fleet struct {
	mu       sync.Mutex
	sessions map[string]*entry
	pids     map[int]string // claude PID → session_id
	account  model.Account
	git      GitLookup
	gitCache map[string]gitCached
}

func New(git GitLookup) *Fleet {
	return &Fleet{sessions: map[string]*entry{}, pids: map[int]string{}, git: git, gitCache: map[string]gitCached{}}
}

func (f *Fleet) Restore(sessions []model.Session, acct model.Account) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range sessions {
		f.sessions[s.SessionID] = &entry{S: s}
	}
	f.account = acct
}

func (f *Fleet) Account() model.Account {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.account
}

func (f *Fleet) Normalize(env model.Envelope, now int64) (model.Event, bool) {
	ts := env.TS
	if ts <= 0 || ts > now+clockSkew || ts < now-clockSkew {
		ts = now
	}
	raw := env.Payload
	if len(raw) == 0 || !json.Valid(raw) {
		raw = json.RawMessage(`{}`)
	}
	raw = trimPayload(raw)
	p := parse(raw)

	source := env.Source
	switch source {
	case model.SourceCLI, model.SourceHook, model.SourceStatusline, model.SourceAPI:
	default:
		source = model.SourceAPI
	}
	kind := env.Kind
	switch source {
	case model.SourceHook:
		kind = model.KindForHook(p.HookEvent)
	case model.SourceStatusline:
		kind = model.KindStatus
	}
	if kind == "" {
		kind = model.KindOther
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	sid, attr := f.resolve(env, source, p, now)
	if kind == model.KindStatus {
		if e := f.sessions[sid]; e != nil && e.statusHash == hash(raw) {
			return model.Event{}, false
		}
	}
	return model.Event{TS: ts, SessionID: sid, Source: source, Kind: kind, Payload: raw, Attribution: attr}, true
}

func hash(b []byte) uint64 {
	h := fnv.New64a()
	h.Write(b)
	return h.Sum64()
}

func (f *Fleet) resolve(env model.Envelope, source string, p payload, now int64) (string, string) {
	learn := func(sid string) {
		if env.ClaudePID > 0 && sid != "" {
			f.pids[env.ClaudePID] = sid
		}
	}
	if (source == model.SourceHook || source == model.SourceStatusline) && p.SessionID != "" {
		learn(p.SessionID)
		return p.SessionID, model.AttrNative
	}
	if env.SessionID != "" {
		if env.SessionVia == model.AttrEnv {
			return env.SessionID, model.AttrEnv
		}
		learn(env.SessionID)
		return env.SessionID, model.AttrNative
	}
	if sid, ok := f.pids[env.ClaudePID]; ok && env.ClaudePID > 0 {
		return sid, model.AttrPtree
	}
	cwd := p.cwd()
	if sid := f.matchCwd(cwd, now); sid != "" {
		return sid, model.AttrCwd
	}
	if cwd == "" {
		return "unknown", model.AttrCwd
	}
	sum := sha1.Sum([]byte(filepath.Clean(cwd)))
	return "cwd-" + hex.EncodeToString(sum[:])[:8], model.AttrCwd
}

// matchCwd picks the most plausible live session for a working directory:
// exact cwd > caller in a subdirectory, then same git branch, then most recent.
func (f *Fleet) matchCwd(cwd string, now int64) string {
	if cwd == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	branch := f.gitInfo(cwd, now).Branch
	best, bestScore, bestLast := "", 0, int64(0)
	for sid, e := range f.sessions {
		if e.S.Cwd == "" {
			continue
		}
		if st, _ := derive.Status(&e.T, now); st == derive.StatusDone {
			continue
		}
		sc := filepath.Clean(e.S.Cwd)
		score := 0
		switch {
		case sc == cwd:
			score = 4
		case strings.HasPrefix(cwd, sc+string(filepath.Separator)):
			score = 2
		default:
			continue
		}
		if branch != "" && e.S.Branch == branch {
			score++
		}
		if score > bestScore || (score == bestScore && e.S.LastEventAt > bestLast) {
			best, bestScore, bestLast = sid, score, e.S.LastEventAt
		}
	}
	return best
}

func (f *Fleet) gitInfo(cwd string, now int64) gitinfo.Info {
	if c, ok := f.gitCache[cwd]; ok && now-c.at < gitCacheTTL {
		return c.info
	}
	info := f.git(cwd)
	f.gitCache[cwd] = gitCached{info, now}
	return info
}

func (f *Fleet) Apply(ev model.Event, now int64) model.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := parse(ev.Payload)
	e := f.sessions[ev.SessionID]
	if e == nil {
		e = &entry{S: model.Session{SessionID: ev.SessionID, StartedAt: ev.TS}}
		f.sessions[ev.SessionID] = e
	}
	s, t := &e.S, &e.T
	if ev.TS > s.LastEventAt {
		s.LastEventAt = ev.TS
	}
	if s.StartedAt == 0 || ev.TS < s.StartedAt {
		s.StartedAt = ev.TS
	}
	if cwd := p.cwd(); cwd != "" && cwd != s.Cwd {
		s.Cwd = cwd
		gi := f.gitInfo(cwd, now)
		s.Repo, s.Branch = gi.Repo, gi.Branch
		s.Name = filepath.Base(filepath.Clean(cwd))
	}
	if model.IsActivity(ev.Kind) {
		t.Activity(ev.TS)
	}

	switch ev.Kind {
	case model.KindSessionStart:
		if m := p.modelName(); m != "" {
			s.Model = m
		}
		t.DoneAt = 0
		t.ClearPending(ev.TS)
	case model.KindStart:
		s.Task, s.StepN, s.StepI, s.StepText = p.Text, p.Steps, 0, ""
		t.DoneAt, t.Blocked, t.ProgressAt = 0, nil, ev.TS
	case model.KindStep:
		s.StepText = p.Text
		t.Blocked, t.ProgressAt = nil, ev.TS
	case model.KindProgress:
		s.StepI = p.I
		if p.N > 0 {
			s.StepN = p.N
		}
		t.Blocked, t.ProgressAt = nil, ev.TS
	case model.KindBlocked:
		t.Blocked = &derive.Need{Type: "blocked", Text: p.Text, TS: ev.TS}
	case model.KindDone:
		t.Blocked = nil
		if ev.TS > t.DoneAt {
			t.DoneAt = ev.TS
		}
		if p.Text != "" {
			s.StepText = p.Text
		}
		if s.StepN > 0 {
			s.StepI = s.StepN
		}
	case model.KindPrompt:
		t.ClearPending(ev.TS)
		t.Blocked, t.DoneAt = nil, 0
	case model.KindToolPre:
		t.ToolStart(f.call(p, s.Cwd, ev.TS))
	case model.KindToolPost, model.KindToolFail:
		c := f.call(p, s.Cwd, 0)
		c.End, c.Failed = ev.TS, ev.Kind == model.KindToolFail
		t.ToolEnd(c)
		t.ClearPending(ev.TS)
	case model.KindPermission:
		sum, _, _, _ := derive.Describe(p.ToolName, p.ToolInput, s.Cwd)
		text := p.ToolName
		if sum != "" {
			text += ": " + sum
		}
		t.SetPending(derive.Need{Type: "permission", Text: text, TS: ev.TS})
	case model.KindNotification:
		switch p.NotificationType {
		case "permission_prompt":
			t.SetPending(derive.Need{Type: "permission", Text: p.Message, TS: ev.TS, Weak: true})
		case "elicitation_dialog":
			t.SetPending(derive.Need{Type: "question", Text: p.Message, TS: ev.TS, Weak: true})
		}
	case model.KindStop, model.KindSessionEnd:
		if ev.TS > t.DoneAt {
			t.DoneAt = ev.TS
		}
		t.ClearPending(ev.TS)
	case model.KindStatus:
		e.statusHash = hash(ev.Payload)
		f.applyStatus(s, p, ev.TS)
	}
	t.Prune(now)
	s.Status, _ = derive.Status(t, now)
	return *s
}

func (f *Fleet) call(p payload, cwd string, start int64) derive.ToolCall {
	sum, key, file, edit := derive.Describe(p.ToolName, p.ToolInput, cwd)
	return derive.ToolCall{ID: p.ToolUseID, Tool: p.ToolName, Summary: sum, Key: key, File: file, Edit: edit, Start: start}
}

func (f *Fleet) applyStatus(s *model.Session, p payload, ts int64) {
	if m := p.modelName(); m != "" {
		s.Model = m
	}
	if cw := p.ContextWindow; cw != nil {
		if cw.UsedPercentage != nil {
			v := *cw.UsedPercentage
			s.CtxPct = &v
		}
		if cw.ContextWindowSize != nil {
			s.CtxSize = int64(*cw.ContextWindowSize)
		}
		if cw.TotalInputTokens != nil {
			s.TokensIn = int64(*cw.TotalInputTokens)
		}
		if cw.TotalOutputTokens != nil {
			s.TokensOut = int64(*cw.TotalOutputTokens)
		}
	}
	if p.Cost != nil && p.Cost.TotalCostUSD != nil {
		s.CostUSD = *p.Cost.TotalCostUSD
	}
	rl := p.RateLimits
	if rl == nil || ts < f.account.UpdatedAt {
		return
	}
	set := func(w *window, pct **float64, reset *int64) bool {
		if w == nil || w.UsedPercentage == nil {
			return false
		}
		v := *w.UsedPercentage
		*pct = &v
		if w.ResetsAt != nil {
			*reset = int64(*w.ResetsAt) * 1000
		}
		return true
	}
	a := set(rl.FiveHour, &f.account.FiveHPct, &f.account.FiveHReset)
	b := set(rl.SevenDay, &f.account.SevenDPct, &f.account.SevenDReset)
	if a || b {
		f.account.UpdatedAt = ts
	}
}
```

`internal/fleet/view.go`:
```go
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
	hideAfter     = 12 * 3600 * 1000
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
	Need      *derive.Need      `json:"need,omitempty"`
	Ctx       derive.CtxBand    `json:"ctx"`
	Calls     []derive.ToolCall `json:"calls,omitempty"`
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
}

func (f *Fleet) View(now int64) View {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := View{Now: now, Account: f.account, Sessions: []SessionView{}, Needs: []NeedItem{}, Collisions: []CollisionView{}}
	var touches []derive.FileTouch
	for _, e := range f.sessions {
		sv := buildView(e, now)
		if (sv.Status == derive.StatusDone || sv.Status == derive.StatusIdle) && now-sv.LastEventAt > hideAfter {
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
	sort.Slice(v.Sessions, func(i, j int) bool {
		a, b := v.Sessions[i], v.Sessions[j]
		if ra, rb := rank(a.Status), rank(b.Status); ra != rb {
			return ra < rb
		}
		if a.LastEventAt != b.LastEventAt {
			return a.LastEventAt > b.LastEventAt
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
	for i := len(t.Calls) - 1; i >= 0 && len(sv.Recent) < 3; i-- {
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

func rank(status string) int {
	switch status {
	case derive.StatusWaiting, derive.StatusBlocked, derive.StatusLooping:
		return 0
	case derive.StatusWorking:
		return 1
	case derive.StatusIdle:
		return 2
	}
	return 3
}

func disambiguate(ss []SessionView) {
	count := map[string]int{}
	for _, s := range ss {
		count[s.Name]++
	}
	for i := range ss {
		if count[ss[i].Name] > 1 {
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
```

- [ ] **Step 4: Run** — `go test ./internal/fleet -v` → PASS. (Debug any failure with superpowers:systematic-debugging; don't weaken the test.)
- [ ] **Step 5: Commit** — `git add internal/fleet && git commit -m "feat(fleet): attribution, materializer and fleet view"`

---

### Task 6: Daemon — HTTP API, guard, SSE hub, run loop

**Files:**
- Create: `web/embed.go`, `web/index.html` (minimal; Task 11 replaces it), `internal/daemon/hub.go`, `internal/daemon/server.go`, `internal/daemon/run.go`
- Test: `internal/daemon/server_test.go`, `internal/daemon/run_test.go`

**Interfaces:**
- Consumes: `store.*`, `fleet.*`, `gitinfo.Lookup`, `version.Version`.
- Produces:
  - `web.FS embed.FS`
  - `daemon.NewServer(st *store.Store, fl *fleet.Fleet, logger *log.Logger) *Server`, `(*Server) Handler() http.Handler`, `(*Server) Loop(ctx context.Context)`
  - `daemon.Run(ctx context.Context, o Options) error`, `type Options struct{ Addr, DataDir string; Logger *log.Logger; OnListen func(addr string) }`, `var ErrAlreadyRunning`
  - Pid file `<DataDir>/trk.pid` while running.
  - HTTP: `POST /v1/events` (202 stored, 204 dropped duplicate, 400 bad JSON, 403 guard), `GET /v1/sessions` (View JSON), `GET /v1/sessions/{id}` (`{"session":SessionView,"events":[...200 newest]}`), `GET /v1/account`, `GET /v1/stream` (SSE `event: snapshot`), `GET /healthz` (`{"ok":true,"app":"trk","version":"..."}`), `GET /` (embedded UI).

- [ ] **Step 1: Minimal web package**

`web/embed.go`:
```go
// Package web embeds the dashboard (no build step).
package web

import "embed"

//go:embed index.html
var FS embed.FS
```
`web/index.html`:
```html
<!doctype html><meta charset="utf-8"><title>TRK.EXE</title><p>TRK.EXE</p>
```
(Task 11 widens the embed pattern to `index.html app.js theme.css fonts`.)

- [ ] **Step 2: Failing tests**

`internal/daemon/server_test.go`:
```go
package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GujaLomsadze/trk/internal/fleet"
	"github.com/GujaLomsadze/trk/internal/gitinfo"
	"github.com/GujaLomsadze/trk/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "trk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := NewServer(st, fleet.New(gitinfo.Lookup), log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Loop(ctx)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

func post(t *testing.T, url, body string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", url+"/v1/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

const permEvent = `{"source":"hook","payload":{"session_id":"S1","hook_event_name":"PermissionRequest","cwd":"/tmp/proj","tool_name":"Bash","tool_input":{"command":"npm test"}}}`

func TestPostAndQuery(t *testing.T) {
	ts, _ := newTestServer(t)
	if c := post(t, ts.URL, permEvent, nil); c != http.StatusAccepted {
		t.Fatalf("post = %d", c)
	}
	var v fleet.View
	getJSON(t, ts.URL+"/v1/sessions", &v)
	if len(v.Sessions) != 1 || v.Sessions[0].Status != "waiting" || len(v.Needs) != 1 {
		t.Fatalf("view = %+v", v)
	}
	var detail struct {
		Session fleet.SessionView `json:"session"`
		Events  []json.RawMessage `json:"events"`
	}
	getJSON(t, ts.URL+"/v1/sessions/S1", &detail)
	if detail.Session.SessionID != "S1" || len(detail.Events) != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	resp, _ := http.Get(ts.URL + "/v1/sessions/nope")
	if resp.StatusCode != 404 {
		t.Fatalf("missing session = %d", resp.StatusCode)
	}
}

func TestBadAndDuplicate(t *testing.T) {
	ts, _ := newTestServer(t)
	if c := post(t, ts.URL, `{not json`, nil); c != 400 {
		t.Fatalf("bad json = %d", c)
	}
	sl := `{"source":"statusline","payload":{"session_id":"S1","context_window":{"used_percentage":12}}}`
	if c := post(t, ts.URL, sl, nil); c != 202 {
		t.Fatalf("first status = %d", c)
	}
	if c := post(t, ts.URL, sl, nil); c != 204 {
		t.Fatalf("duplicate status = %d", c)
	}
}

func TestGuard(t *testing.T) {
	ts, _ := newTestServer(t)
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"cli (no origin)", nil, 202},
		{"dashboard origin", map[string]string{"Origin": "http://localhost:7777"}, 202},
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, 403},
		{"null origin", map[string]string{"Origin": "null"}, 403},
	}
	for _, c := range cases {
		if got := post(t, ts.URL, permEvent, c.hdr); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	req, _ := http.NewRequest("GET", ts.URL+"/v1/sessions", nil)
	req.Host = "attacker.example:7777" // DNS rebinding
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("rebinding host: %v %v", resp.StatusCode, err)
	}
}

func TestHealthzAndIndex(t *testing.T) {
	ts, _ := newTestServer(t)
	var h map[string]any
	getJSON(t, ts.URL+"/healthz", &h)
	if h["app"] != "trk" || h["ok"] != true {
		t.Fatalf("healthz = %v", h)
	}
	resp, _ := http.Get(ts.URL + "/")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "TRK.EXE") {
		t.Fatalf("index = %d %q", resp.StatusCode, b)
	}
}

func TestStreamDeliversWithinOneSecond(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	frames := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if l := sc.Text(); strings.HasPrefix(l, "data: ") {
				frames <- l[6:]
			}
		}
	}()
	select {
	case <-frames: // initial snapshot
	case <-time.After(time.Second):
		t.Fatal("no initial snapshot")
	}
	start := time.Now()
	post(t, ts.URL, permEvent, nil)
	for {
		select {
		case f := <-frames:
			if strings.Contains(f, `"needs_you":1`) {
				if d := time.Since(start); d > time.Second {
					t.Fatalf("took %v", d)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("no update within 1s")
		}
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}
```

`internal/daemon/run_test.go`:
```go
package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestRunSingleInstanceAndPidFile(t *testing.T) {
	addr, dir := freeAddr(t), t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Addr: addr, DataDir: dir, Logger: log.New(io.Discard, "", 0), OnListen: func(string) { close(ready) }})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not start")
	}
	if _, err := os.Stat(filepath.Join(dir, "trk.pid")); err != nil {
		t.Fatalf("pid file: %v", err)
	}
	err := Run(context.Background(), Options{Addr: addr, DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run = %v, want ErrAlreadyRunning", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown hung")
	}
	if _, err := os.Stat(filepath.Join(dir, "trk.pid")); !os.IsNotExist(err) {
		t.Fatal("pid file not removed")
	}
}

func TestRunPortTakenByStranger(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go http.Serve(l, http.NotFoundHandler())
	err := Run(context.Background(), Options{Addr: l.Addr().String(), DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if err == nil || errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want a listen error", err)
	}
}
```

- [ ] **Step 3: Run** — `go test ./internal/daemon` → FAIL.

- [ ] **Step 4: Implement**

`internal/daemon/hub.go`:
```go
package daemon

import "sync"

// Hub fans snapshots out to SSE clients. Each client holds at most one pending
// snapshot; a newer one replaces it (snapshots are full state, so dropping is safe).
type Hub struct {
	mu   sync.Mutex
	subs map[chan []byte]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[chan []byte]struct{}{}} }

func (h *Hub) Subscribe() (<-chan []byte, func()) {
	ch := make(chan []byte, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}

func (h *Hub) Publish(b []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- b:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- b:
			default:
			}
		}
	}
}
```

`internal/daemon/server.go`:
```go
// Package daemon serves the TRK HTTP API, SSE stream and dashboard.
package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/GujaLomsadze/trk/internal/fleet"
	"github.com/GujaLomsadze/trk/internal/model"
	"github.com/GujaLomsadze/trk/internal/store"
	"github.com/GujaLomsadze/trk/internal/version"
	"github.com/GujaLomsadze/trk/web"
)

const maxBody = 4 << 20

type Server struct {
	st    *store.Store
	fl    *fleet.Fleet
	hub   *Hub
	log   *log.Logger
	now   func() int64
	dirty atomic.Bool
}

func NewServer(st *store.Store, fl *fleet.Fleet, logger *log.Logger) *Server {
	return &Server{st: st, fl: fl, hub: NewHub(), log: logger, now: func() int64 { return time.Now().UnixMilli() }}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", s.postEvent)
	mux.HandleFunc("GET /v1/sessions", s.getSessions)
	mux.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	mux.HandleFunc("GET /v1/account", s.getAccount)
	mux.HandleFunc("GET /v1/stream", s.stream)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.Handle("GET /", http.FileServerFS(web.FS))
	return guard(mux)
}

// guard blocks DNS rebinding (foreign Host) and cross-site requests (foreign Origin).
// The CLI sends no Origin; the dashboard is same-origin.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !localHost(u.Host) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	switch strings.Trim(host, "[]") {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func (s *Server) postEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	var env model.Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	now := s.now()
	ev, ok := s.fl.Normalize(env, now)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.st.Append(&ev); err != nil {
		s.log.Printf("append: %v", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	sess := s.fl.Apply(ev, now)
	if err := s.st.UpsertSession(sess); err != nil {
		s.log.Printf("upsert session: %v", err)
	}
	if ev.Kind == model.KindStatus {
		if err := s.st.SaveAccount(s.fl.Account()); err != nil {
			s.log.Printf("save account: %v", err)
		}
	}
	s.dirty.Store(true)
	w.WriteHeader(http.StatusAccepted)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) getSessions(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.fl.View(s.now())) }

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sv, ok := s.fl.SessionView(id, s.now())
	if !ok {
		http.NotFound(w, r)
		return
	}
	evs, err := s.st.SessionEvents(id, 200)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if evs == nil {
		evs = []model.Event{}
	}
	writeJSON(w, map[string]any{"session": sv, "events": evs})
}

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.fl.Account()) }

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true, "app": "trk", "version": version.Version})
}

func (s *Server) snapshot() []byte {
	b, err := json.Marshal(s.fl.View(s.now()))
	if err != nil {
		s.log.Printf("snapshot: %v", err)
		return []byte("{}")
	}
	return b
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	ch, cancel := s.hub.Subscribe()
	defer cancel()
	send := func(b []byte) error {
		if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", b); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}
	if send(s.snapshot()) != nil {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			if send(b) != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
```

Add to `server.go` (same package, needs `"context"` import):
```go
// Loop coalesces updates into ≤4 snapshots/s and refreshes every 5 s so
// time-based states (idle, ages, countdowns) advance without new events.
func (s *Server) Loop(ctx context.Context) {
	flush := time.NewTicker(250 * time.Millisecond)
	tick := time.NewTicker(5 * time.Second)
	defer flush.Stop()
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.dirty.Store(true)
		case <-flush.C:
			if s.dirty.Swap(false) {
				s.hub.Publish(s.snapshot())
			}
		}
	}
}
```

`internal/daemon/run.go`:
```go
package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/fleet"
	"github.com/GujaLomsadze/trk/internal/gitinfo"
	"github.com/GujaLomsadze/trk/internal/store"
)

var ErrAlreadyRunning = errors.New("trk daemon already running")

type Options struct {
	Addr     string
	DataDir  string
	Logger   *log.Logger
	OnListen func(addr string)
}

// Run binds first (the port is the single-instance lock), then opens the store,
// rebuilds state from the last 30 minutes of events, and serves until ctx ends.
func Run(ctx context.Context, o Options) error {
	if o.Logger == nil {
		o.Logger = log.New(io.Discard, "", 0)
	}
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		if isTrk(o.Addr) {
			return ErrAlreadyRunning
		}
		return fmt.Errorf("listen %s: %w", o.Addr, err)
	}
	if err := os.MkdirAll(o.DataDir, 0o755); err != nil {
		ln.Close()
		return err
	}
	st, err := store.Open(filepath.Join(o.DataDir, "trk.db"))
	if err != nil {
		ln.Close()
		return err
	}
	defer st.Close()

	fl := fleet.New(gitinfo.Lookup)
	if err := restore(st, fl, time.Now().UnixMilli()); err != nil {
		o.Logger.Printf("restore: %v", err) // degrade to empty state, keep serving
	}
	pidFile := filepath.Join(o.DataDir, "trk.pid")
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(pidFile)

	srv := NewServer(st, fl, o.Logger)
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second} // no WriteTimeout: SSE
	loopCtx, stopLoop := context.WithCancel(ctx)
	defer stopLoop()
	go srv.Loop(loopCtx)
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if hs.Shutdown(sctx) != nil {
			hs.Close() // SSE connections never go idle
		}
	}()
	o.Logger.Printf("trk listening on http://%s", ln.Addr())
	if o.OnListen != nil {
		o.OnListen(ln.Addr().String())
	}
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func restore(st *store.Store, fl *fleet.Fleet, now int64) error {
	ss, err := st.Sessions()
	if err != nil {
		return err
	}
	acct, err := st.Account()
	if err != nil {
		return err
	}
	fl.Restore(ss, acct)
	evs, err := st.EventsSince(now - 30*60*1000)
	if err != nil {
		return err
	}
	for _, ev := range evs {
		fl.Apply(ev, now)
	}
	return nil
}

func isTrk(addr string) bool {
	c := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.Contains(string(b), `"app":"trk"`)
}
```

- [ ] **Step 5: Run** — `go test ./internal/daemon -v -race` → PASS.
- [ ] **Step 6: Commit** — `git add web internal/daemon && git commit -m "feat(daemon): http api, sse stream and single-instance run loop"`

---

### Task 7: ptree — find the ancestor Claude process

**Files:**
- Create: `internal/ptree/ptree.go`, `internal/ptree/ptree_linux.go`, `internal/ptree/ptree_darwin.go`, `internal/ptree/ptree_windows.go`, `internal/ptree/ptree_other.go`
- Test: `internal/ptree/ptree_test.go`, `internal/ptree/ptree_linux_test.go`

**Interfaces:**
- Produces: `ptree.FindClaude(start int) int` — PID of the nearest process at or above `start` that is Claude Code, else 0. CLI calls `FindClaude(os.Getppid())`.

Verified on this machine (WSL2, native installer): the Claude process has `comm=claude`, and Bash tool commands run as `claude → zsh → <cmd>`. npm installs show up as `node …/@anthropic-ai/claude-code/cli.js`; that's matched via cmdline on Linux only (on macOS/Windows npm installs fall back to cwd attribution, which is fine).

- [ ] **Step 1: Add dependency** — `go get golang.org/x/sys@latest`

- [ ] **Step 2: Failing tests**

`internal/ptree/ptree_test.go`:
```go
package ptree

import "testing"

func TestFindClaude(t *testing.T) {
	table := map[int]proc{
		100: {PPID: 1, Name: "zsh"},
		200: {PPID: 100, Name: "claude"},
		300: {PPID: 200, Name: "zsh"},
		400: {PPID: 300, Name: "trk"},
		500: {PPID: 100, Name: "node", Cmdline: "node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"},
		600: {PPID: 500, Name: "bash"},
		700: {PPID: 100, Name: "node", Cmdline: "node server.js"},
		800: {PPID: 700, Name: "sh"},
		900: {PPID: 900, Name: "loop"},
		950: {PPID: 1, Name: "Claude.exe"},
		960: {PPID: 950, Name: "cmd.exe"},
	}
	lookup := func(pid int) (proc, bool) { p, ok := table[pid]; return p, ok }
	cases := map[int]int{400: 200, 300: 200, 200: 200, 600: 500, 800: 0, 900: 0, 960: 950, 12345: 0, 1: 0, 0: 0}
	for start, want := range cases {
		if got := findClaude(start, lookup); got != want {
			t.Errorf("findClaude(%d) = %d, want %d", start, got, want)
		}
	}
}

func TestParseStat(t *testing.T) {
	p, ok := parseStat([]byte("1234 (tmux: server (x)) S 77 1234 1234 0 -1"))
	if !ok || p.PPID != 77 || p.Name != "tmux: server (x)" {
		t.Fatalf("parseStat = %+v %v", p, ok)
	}
	if _, ok := parseStat([]byte("garbage")); ok {
		t.Fatal("garbage parsed")
	}
}
```
(`parseStat` lives in `ptree.go` — pure, compiled on all OSes, so this test runs everywhere.)

`internal/ptree/ptree_linux_test.go`:
```go
package ptree

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A copy of /bin/sh named "claude" stands in for Claude Code; we look up from its child.
func TestFindClaudeRealProcesses(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "claude")
	src, err := os.Open("/bin/sh")
	if err != nil {
		t.Skip("no /bin/sh")
	}
	dst, _ := os.OpenFile(fake, os.O_CREATE|os.O_WRONLY, 0o755)
	io.Copy(dst, src)
	src.Close()
	dst.Close()

	cmd := exec.Command(fake, "-c", "sleep 30; true") // "; true" stops sh from exec-ing sleep in place
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	var child int
	deadline := time.Now().Add(3 * time.Second)
	for child == 0 && time.Now().Before(deadline) {
		ents, _ := os.ReadDir("/proc")
		for _, e := range ents {
			var pid int
			if _, err := fmtSscan(e.Name(), &pid); err != nil {
				continue
			}
			if p, ok := readProc(pid); ok && p.PPID == cmd.Process.Pid {
				child = pid
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("child of fake claude not found")
	}
	if got := FindClaude(child); got != cmd.Process.Pid {
		t.Fatalf("FindClaude(%d) = %d, want %d", child, got, cmd.Process.Pid)
	}
	if got := FindClaude(os.Getpid()); got != 0 && got == cmd.Process.Pid {
		t.Fatal("test process wrongly attributed to fake claude")
	}
}
```
with helper in the same test file:
```go
func fmtSscan(s string, pid *int) (int, error) { return fmt.Sscan(s, pid) }
```
(add `"fmt"` to the imports.)

- [ ] **Step 3: Run** — `go test ./internal/ptree` → FAIL.

- [ ] **Step 4: Implement**

`internal/ptree/ptree.go`:
```go
// Package ptree walks parent processes to find the Claude Code process a command runs under.
package ptree

import (
	"path/filepath"
	"strconv"
	"strings"
)

type proc struct {
	PPID    int
	Name    string
	Cmdline string
}

func FindClaude(start int) int {
	if start <= 1 {
		return 0
	}
	return findClaude(start, snapshot())
}

func findClaude(pid int, lookup func(int) (proc, bool)) int {
	for depth := 0; pid > 1 && depth < 64; depth++ {
		p, ok := lookup(pid)
		if !ok {
			return 0
		}
		if isClaude(p) {
			return pid
		}
		if p.PPID == pid {
			return 0
		}
		pid = p.PPID
	}
	return 0
}

func isClaude(p proc) bool {
	name := strings.ToLower(filepath.Base(strings.ReplaceAll(p.Name, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "claude":
		return true
	case "node", "bun":
		c := strings.ToLower(p.Cmdline)
		return strings.Contains(c, "@anthropic-ai/claude-code") || strings.Contains(c, "/bin/claude")
	}
	return false
}

// parseStat parses /proc/<pid>/stat. The comm field may contain spaces and parens.
func parseStat(b []byte) (proc, bool) {
	s := string(b)
	l, r := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if l < 0 || r < l {
		return proc{}, false
	}
	f := strings.Fields(s[r+1:])
	if len(f) < 2 {
		return proc{}, false
	}
	ppid, err := strconv.Atoi(f[1])
	if err != nil {
		return proc{}, false
	}
	return proc{PPID: ppid, Name: s[l+1 : r]}, true
}
```

`internal/ptree/ptree_linux.go`:
```go
package ptree

import (
	"fmt"
	"os"
	"strings"
)

func snapshot() func(int) (proc, bool) { return readProc }

func readProc(pid int) (proc, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return proc{}, false
	}
	p, ok := parseStat(b)
	if !ok {
		return p, false
	}
	if p.Name == "node" || p.Name == "bun" {
		if c, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid)); err == nil {
			p.Cmdline = strings.ReplaceAll(string(c), "\x00", " ")
		}
	}
	return p, true
}
```

`internal/ptree/ptree_darwin.go`:
```go
package ptree

import "golang.org/x/sys/unix"

func snapshot() func(int) (proc, bool) {
	return func(pid int) (proc, bool) {
		kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
		if err != nil || kp.Proc.P_pid == 0 {
			return proc{}, false
		}
		return proc{PPID: int(kp.Eproc.Ppid), Name: unix.ByteSliceToString(kp.Proc.P_comm[:])}, true
	}
}
```
(If `P_comm` is `[17]int8` in the pinned x/sys version, convert byte-by-byte. Verify with `GOOS=darwin GOARCH=arm64 go vet ./internal/ptree`.)

`internal/ptree/ptree_windows.go`:
```go
package ptree

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

func snapshot() func(int) (proc, bool) {
	m := map[int]proc{}
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err == nil {
		defer windows.CloseHandle(h)
		var e windows.ProcessEntry32
		e.Size = uint32(unsafe.Sizeof(e))
		for err := windows.Process32First(h, &e); err == nil; err = windows.Process32Next(h, &e) {
			m[int(e.ProcessID)] = proc{PPID: int(e.ParentProcessID), Name: windows.UTF16ToString(e.ExeFile[:])}
		}
	}
	return func(pid int) (proc, bool) { p, ok := m[pid]; return p, ok }
}
```

`internal/ptree/ptree_other.go`:
```go
//go:build !linux && !darwin && !windows

package ptree

func snapshot() func(int) (proc, bool) { return func(int) (proc, bool) { return proc{}, false } }
```

- [ ] **Step 5: Run** — `go test ./internal/ptree -v` and cross-vet: `for os in darwin windows freebsd; do GOOS=$os GOARCH=arm64 go vet ./internal/ptree || exit 1; done` → PASS / clean.
- [ ] **Step 6: Commit** — `git add internal/ptree go.mod go.sum && git commit -m "feat(ptree): locate ancestor claude process on linux, macos, windows"`

---

### Task 8: client — 200 ms POST, auto-spawn, never fail

**Files:**
- Create: `internal/client/client.go`, `internal/client/spawn_unix.go` (`//go:build !windows`), `internal/client/spawn_windows.go`
- Test: `internal/client/client_test.go`

**Interfaces:**
- Consumes: `config.BaseURL/IsLocalDefault/DataDir`, `model.Envelope`.
- Produces: `type Client struct{ BaseURL string; HTTP *http.Client; AllowSpawn bool; Spawn func() error; SpawnBudget time.Duration }`, `Default() *Client`, `(*Client) Send(model.Envelope) bool`, `(*Client) Healthy() bool`, `(*Client) EnsureDaemon(wait time.Duration) bool`, `SpawnDaemon() error`.

Rules: `HTTP.Timeout = 200ms`. Spawn only when `AllowSpawn` (`TRK_URL` unset and `TRK_NO_SPAWN` unset) **and** the error is a dial error (`*net.OpError` with `Op=="dial"`). After spawning, retry every 50 ms until `SpawnBudget` (1 s). Status ≥400 counts as failure but never triggers spawn. `SpawnDaemon` runs `os.Executable() serve` with stdio nil, `Dir` = home dir (don't pin a worktree on Windows), detached (`Setsid` on unix; `CREATE_NEW_PROCESS_GROUP|DETACHED_PROCESS` + `HideWindow` on Windows), then `Process.Release()`.

- [ ] **Step 1: Failing tests** (`client_test.go`)

```go
package client

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GujaLomsadze/trk/internal/model"
)

func deadURL(t *testing.T) string {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	u := "http://" + l.Addr().String()
	l.Close()
	return u
}

func TestSendDelivers(t *testing.T) {
	var got atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" && r.Method == "POST" {
			got.Add(1)
		}
		w.WriteHeader(202)
	}))
	defer ts.Close()
	c := &Client{BaseURL: ts.URL, HTTP: &http.Client{Timeout: 200 * time.Millisecond}}
	if !c.Send(model.Envelope{Source: "cli", Kind: "step"}) || got.Load() != 1 {
		t.Fatal("not delivered")
	}
}

func TestSendDownNoSpawnIsFastAndSilent(t *testing.T) {
	c := &Client{BaseURL: deadURL(t), HTTP: &http.Client{Timeout: 200 * time.Millisecond}}
	start := time.Now()
	if c.Send(model.Envelope{Source: "cli"}) {
		t.Fatal("reported delivered")
	}
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Fatalf("took %v", d)
	}
}

func TestSendHangingServerRespectsTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }))
	defer ts.Close()
	var spawned atomic.Int32
	c := &Client{BaseURL: ts.URL, HTTP: &http.Client{Timeout: 200 * time.Millisecond}, AllowSpawn: true,
		Spawn: func() error { spawned.Add(1); return nil }, SpawnBudget: time.Second}
	start := time.Now()
	c.Send(model.Envelope{Source: "cli"})
	if d := time.Since(start); d > 400*time.Millisecond || spawned.Load() != 0 {
		t.Fatalf("took %v, spawned %d (timeout must not spawn)", d, spawned.Load())
	}
}

func TestSendSpawnsThenDelivers(t *testing.T) {
	url := deadURL(t)
	addr := url[len("http://"):]
	var spawned atomic.Int32
	var srv *http.Server
	c := &Client{BaseURL: url, HTTP: &http.Client{Timeout: 200 * time.Millisecond}, AllowSpawn: true, SpawnBudget: time.Second,
		Spawn: func() error {
			spawned.Add(1)
			go func() {
				time.Sleep(150 * time.Millisecond) // daemon boot time
				l, err := net.Listen("tcp", addr)
				if err != nil {
					return
				}
				srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })}
				srv.Serve(l)
			}()
			return nil
		}}
	if !c.Send(model.Envelope{Source: "cli"}) || spawned.Load() != 1 {
		t.Fatalf("spawned=%d", spawned.Load())
	}
	if srv != nil {
		srv.Close()
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/client` → FAIL.

- [ ] **Step 3: Implement** `client.go`:

```go
// Package client sends events to the daemon. It never returns errors to callers:
// TRK must not break an agent.
package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"time"

	"github.com/GujaLomsadze/trk/internal/config"
	"github.com/GujaLomsadze/trk/internal/model"
)

const Timeout = 200 * time.Millisecond

type Client struct {
	BaseURL     string
	HTTP        *http.Client
	AllowSpawn  bool
	Spawn       func() error
	SpawnBudget time.Duration
}

func Default() *Client {
	return &Client{
		BaseURL:     config.BaseURL(),
		HTTP:        &http.Client{Timeout: Timeout},
		AllowSpawn:  config.IsLocalDefault() && os.Getenv("TRK_NO_SPAWN") == "",
		Spawn:       SpawnDaemon,
		SpawnBudget: time.Second,
	}
}

func (c *Client) Send(env model.Envelope) bool {
	body, err := json.Marshal(env)
	if err != nil {
		return false
	}
	err = c.post(body)
	if err == nil {
		return true
	}
	if !c.AllowSpawn || c.Spawn == nil || !isDial(err) || c.Spawn() != nil {
		return false
	}
	for deadline := time.Now().Add(c.SpawnBudget); time.Now().Before(deadline); {
		time.Sleep(50 * time.Millisecond)
		if c.post(body) == nil {
			return true
		}
	}
	return false
}

func (c *Client) post(body []byte) error {
	resp, err := c.HTTP.Post(c.BaseURL+"/v1/events", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) Healthy() bool {
	resp, err := c.HTTP.Get(c.BaseURL + "/healthz")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

// EnsureDaemon is for interactive commands (open/init) that may wait longer.
func (c *Client) EnsureDaemon(wait time.Duration) bool {
	if c.Healthy() {
		return true
	}
	if !c.AllowSpawn || c.Spawn == nil || c.Spawn() != nil {
		return false
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if c.Healthy() {
			return true
		}
	}
	return false
}

func isDial(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

func SpawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "serve")
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
```

`spawn_unix.go`:
```go
//go:build !windows

package client

import (
	"os/exec"
	"syscall"
)

func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
```

`spawn_windows.go`:
```go
package client

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS, HideWindow: true}
}
```

- [ ] **Step 4: Run** — `go test ./internal/client -v && GOOS=windows go vet ./internal/client` → PASS.
- [ ] **Step 5: Commit** — `git commit -am "feat(client): bounded event post with daemon auto-spawn"` (add new files first).

---

### Task 9: CLI — reporting verbs, hook, statusline, serve, open

**Files:**
- Create: `internal/cli/report.go`, `internal/cli/integrations.go`, `internal/cli/serve.go`
- Modify: `internal/cli/cli.go` (dispatch)
- Test: `internal/cli/report_test.go`, `internal/cli/integrations_test.go`

**Interfaces:**
- Consumes: `client.Default`, `ptree.FindClaude`, `daemon.Run`, `config.*`.
- Produces: test seams `var send = func(model.Envelope) bool`, `var findClaude = func() int`, `var nowMS = func() int64`; `parseStart(args []string) (text string, steps int)`, `parseProgress(s string) (i, n int, ok bool)`, `compactLine(raw []byte) string`.

Rules:
- Reporting verbs, `hook`, `statusline` are wrapped by `defer recover()` and **always return 0**. Bad args: one usage line to stderr, exit 0, nothing sent.
- Envelope: `TS=nowMS()`, `ClaudePID=findClaude()`; `TRK_SESSION` → `SessionID` + `SessionVia="env"`. CLI payload: `{"text","steps","i","n","cwd"}` (omitempty).
- `start "<task>" --steps N` accepts `--steps N` / `--steps=N` anywhere; remaining args joined with spaces as text.
- `progress 3/5` or `progress 3`.
- `hook`: read stdin (limit 8 MB); if not valid JSON → send nothing; else envelope `{source:"hook", payload:<stdin>}`. **Print nothing.**
- `statusline [--then CMD]`: read stdin; send in a goroutine (if valid JSON); with `--then`, run CMD via `sh -c` (Windows: `bash -c` if on PATH, else `cmd /C`) with the same stdin bytes, stdout → our stdout, stderr discarded; otherwise print `compactLine`. Wait for the send before exiting.
- `compactLine` → `trk · ctx 42% · 5h 23% · 7d 61%`, `—` for each missing/null value, and `—` for a window whose `resets_at` is in the past.
- `serve`: logs to `<data>/trk.log` (truncate at start if >5 MB); prints `trk serving http://localhost:<port>` via `OnListen`; `ErrAlreadyRunning` → print `trk already running at …`, exit 0; signals via `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)`.
- `open`: `EnsureDaemon(3s)`, print URL, open browser — macOS `open`, Windows `rundll32 url.dll,FileProtocolHandler`, WSL (`/proc/version` contains `microsoft`) `wslview` if present else `cmd.exe /c start "" URL`, else `xdg-open`. Failure to launch a browser is not an error (URL is printed).

- [ ] **Step 1: Failing tests**

`internal/cli/report_test.go`:
```go
package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GujaLomsadze/trk/internal/model"
)

func capture(t *testing.T) *[]model.Envelope {
	t.Helper()
	var got []model.Envelope
	oldSend, oldFind, oldNow := send, findClaude, nowMS
	send = func(e model.Envelope) bool { got = append(got, e); return true }
	findClaude = func() int { return 4242 }
	nowMS = func() int64 { return 1700000000000 }
	t.Cleanup(func() { send, findClaude, nowMS = oldSend, oldFind, oldNow })
	return &got
}

func run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestParseStart(t *testing.T) {
	cases := []struct {
		args  []string
		text  string
		steps int
	}{
		{[]string{"Refactor Kafka consumer", "--steps", "5"}, "Refactor Kafka consumer", 5},
		{[]string{"--steps=3", "Fix", "bug"}, "Fix bug", 3},
		{[]string{"No steps"}, "No steps", 0},
		{[]string{"x", "--steps", "nope"}, "x", 0},
	}
	for _, c := range cases {
		text, steps := parseStart(c.args)
		if text != c.text || steps != c.steps {
			t.Errorf("parseStart(%q) = %q,%d", c.args, text, steps)
		}
	}
}

func TestParseProgress(t *testing.T) {
	cases := map[string][3]int{"3/5": {3, 5, 1}, "3": {3, 0, 1}, " 2 / 4 ": {2, 4, 1}, "x/5": {0, 0, 0}, "": {0, 0, 0}, "-1/5": {0, 0, 0}}
	for in, w := range cases {
		i, n, ok := parseProgress(in)
		if i != w[0] || n != w[1] || ok != (w[2] == 1) {
			t.Errorf("parseProgress(%q) = %d,%d,%v", in, i, n, ok)
		}
	}
}

func TestReportingVerbs(t *testing.T) {
	got := capture(t)
	t.Setenv("TRK_SESSION", "")
	for _, args := range [][]string{
		{"start", "Refactor", "--steps", "5"}, {"step", "Writing tests"}, {"progress", "3/5"},
		{"blocked", "Drop legacy topic?"}, {"done", "Tests green"},
	} {
		if code, out, _ := run(args...); code != 0 || out != "" {
			t.Fatalf("%v: code=%d out=%q", args, code, out)
		}
	}
	if len(*got) != 5 {
		t.Fatalf("sent %d", len(*got))
	}
	e := (*got)[2]
	var p map[string]any
	json.Unmarshal(e.Payload, &p)
	if e.Kind != "progress" || e.Source != "cli" || e.ClaudePID != 4242 || p["i"] != 3.0 || p["n"] != 5.0 || p["cwd"] == "" {
		t.Fatalf("progress envelope = %+v %v", e, p)
	}
}

func TestTrkSessionEnv(t *testing.T) {
	got := capture(t)
	t.Setenv("TRK_SESSION", "my-sess")
	run("step", "x")
	if e := (*got)[0]; e.SessionID != "my-sess" || e.SessionVia != "env" {
		t.Fatalf("envelope = %+v", e)
	}
}

func TestBadArgsStillExitZero(t *testing.T) {
	got := capture(t)
	for _, args := range [][]string{{"progress", "banana"}, {"step"}, {"start"}} {
		if code, _, _ := run(args...); code != 0 {
			t.Fatalf("%v exit %d", args, code)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("sent %d for bad args", len(*got))
	}
}

func TestPanicInSendStillExitZero(t *testing.T) {
	capture(t)
	send = func(model.Envelope) bool { panic("boom") }
	if code, _, _ := run("step", "x"); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
```

`internal/cli/integrations_test.go`:
```go
package cli

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runIn(stdin string, args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String()
}

func TestHookForwardsAndPrintsNothing(t *testing.T) {
	got := capture(t)
	code, out := runIn(`{"session_id":"A","hook_event_name":"Stop"}`, "hook")
	if code != 0 || out != "" || len(*got) != 1 || (*got)[0].Source != "hook" || (*got)[0].ClaudePID != 4242 {
		t.Fatalf("code=%d out=%q got=%+v", code, out, *got)
	}
}

func TestHookGarbageStdin(t *testing.T) {
	got := capture(t)
	for _, in := range []string{"", "not json", "{", strings.Repeat("x", 9<<20)} {
		if code, out := runIn(in, "hook"); code != 0 || out != "" {
			t.Fatalf("code=%d out=%q", code, out)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("sent %d garbage events", len(*got))
	}
}

func TestHookDaemonDownIsFastAndSilent(t *testing.T) {
	t.Setenv("TRK_URL", "http://127.0.0.1:1") // nothing listens; TRK_URL disables spawn
	start := time.Now()
	var out, errb bytes.Buffer
	code := Run([]string{"hook"}, strings.NewReader(`{"session_id":"A"}`), &out, &errb)
	if code != 0 || out.Len() != 0 || errb.Len() != 0 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("code=%d out=%q err=%q took=%v", code, out.String(), errb.String(), time.Since(start))
	}
}

func TestCompactLine(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	cases := []struct{ in, want string }{
		{`{"context_window":{"used_percentage":42.4},"rate_limits":{"five_hour":{"used_percentage":23,"resets_at":` + itoa(future) + `},"seven_day":{"used_percentage":61.2}}}`, "trk · ctx 42% · 5h 23% · 7d 61%"},
		{`{"context_window":{"used_percentage":null}}`, "trk · ctx — · 5h — · 7d —"},
		{`{"rate_limits":{"five_hour":{"used_percentage":90,"resets_at":1}}}`, "trk · ctx — · 5h — · 7d —"},
		{`garbage`, "trk · ctx — · 5h — · 7d —"},
	}
	for _, c := range cases {
		if got := compactLine([]byte(c.in)); got != c.want {
			t.Errorf("compactLine(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatuslineGarbage(t *testing.T) {
	capture(t)
	code, out := runIn("not json", "statusline")
	if code != 0 || strings.TrimSpace(out) != "trk · ctx — · 5h — · 7d —" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestStatuslineThenChains(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	got := capture(t)
	code, out := runIn(`{"session_id":"A","model":{"display_name":"Opus"}}`, "statusline", "--then", `cat | grep -o '"display_name":"[^"]*"'`)
	if code != 0 || strings.TrimSpace(out) != `"display_name":"Opus"` || len(*got) != 1 {
		t.Fatalf("code=%d out=%q sent=%d", code, out, len(*got))
	}
}

func itoa(n int64) string { return strconvFormat(n) }
```
with `func strconvFormat(n int64) string { return strconv.FormatInt(n, 10) }` (import `strconv`).

- [ ] **Step 2: Run** — `go test ./internal/cli` → FAIL.

- [ ] **Step 3: Implement**

`internal/cli/report.go`:
```go
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/client"
	"github.com/GujaLomsadze/trk/internal/model"
	"github.com/GujaLomsadze/trk/internal/ptree"
)

// Seams for tests.
var (
	send       = func(e model.Envelope) bool { return client.Default().Send(e) }
	findClaude = func() int { return ptree.FindClaude(os.Getppid()) }
	nowMS      = func() int64 { return time.Now().UnixMilli() }
)

type cliPayload struct {
	Text  string `json:"text,omitempty"`
	Steps int    `json:"steps,omitempty"`
	I     int    `json:"i,omitempty"`
	N     int    `json:"n,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
}

func envelope(source, kind string, payload json.RawMessage) model.Envelope {
	env := model.Envelope{TS: nowMS(), Source: source, Kind: kind, ClaudePID: findClaude(), Payload: payload}
	if s := os.Getenv("TRK_SESSION"); s != "" {
		env.SessionID, env.SessionVia = s, model.AttrEnv
	}
	return env
}

func report(kind string, args []string, stderr io.Writer) int {
	p := cliPayload{}
	p.Cwd, _ = os.Getwd()
	switch kind {
	case model.KindStart:
		p.Text, p.Steps = parseStart(args)
		if p.Text == "" {
			fmt.Fprintln(stderr, `usage: trk start "<task>" [--steps N]`)
			return 0
		}
	case model.KindStep, model.KindBlocked:
		p.Text = strings.TrimSpace(strings.Join(args, " "))
		if p.Text == "" {
			fmt.Fprintf(stderr, "usage: trk %s \"<text>\"\n", kind)
			return 0
		}
	case model.KindDone:
		p.Text = strings.TrimSpace(strings.Join(args, " "))
	case model.KindProgress:
		i, n, ok := parseProgress(strings.Join(args, ""))
		if !ok {
			fmt.Fprintln(stderr, "usage: trk progress <i>/<N>")
			return 0
		}
		p.I, p.N = i, n
	}
	b, _ := json.Marshal(p)
	send(envelope(model.SourceCLI, kind, b))
	return 0
}

func parseStart(args []string) (string, int) {
	var words []string
	steps := 0
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--steps" && i+1 < len(args):
			steps, _ = strconv.Atoi(args[i+1])
			i++
		case strings.HasPrefix(a, "--steps="):
			steps, _ = strconv.Atoi(strings.TrimPrefix(a, "--steps="))
		default:
			words = append(words, a)
		}
	}
	if steps < 0 {
		steps = 0
	}
	return strings.TrimSpace(strings.Join(words, " ")), steps
}

func parseProgress(s string) (int, int, bool) {
	s = strings.ReplaceAll(s, " ", "")
	is, ns, hasN := strings.Cut(s, "/")
	i, err := strconv.Atoi(is)
	if err != nil || i < 0 {
		return 0, 0, false
	}
	if !hasN {
		return i, 0, true
	}
	n, err := strconv.Atoi(ns)
	if err != nil || n < 0 {
		return 0, 0, false
	}
	return i, n, true
}
```

`internal/cli/integrations.go`:
```go
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"

	"github.com/GujaLomsadze/trk/internal/model"
)

const maxStdin = 8 << 20

func readStdin(r io.Reader) []byte {
	b, _ := io.ReadAll(io.LimitReader(r, maxStdin+1))
	if len(b) > maxStdin {
		return nil
	}
	return b
}

func hook(stdin io.Reader) int {
	b := readStdin(stdin)
	if len(b) == 0 || !json.Valid(b) {
		return 0
	}
	send(envelope(model.SourceHook, "", b))
	return 0
}

func statusline(args []string, stdin io.Reader, stdout io.Writer) int {
	then := ""
	for i := 0; i < len(args); i++ {
		if args[i] == "--then" && i+1 < len(args) {
			then = args[i+1]
			i++
		}
	}
	b := readStdin(stdin)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if len(b) > 0 && json.Valid(b) {
			send(envelope(model.SourceStatusline, "", b))
		}
	}()
	if then != "" {
		runThen(then, b, stdout)
	} else {
		fmt.Fprintln(stdout, compactLine(b))
	}
	<-done
	return 0
}

func runThen(cmdline string, input []byte, stdout io.Writer) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		if bash, err := exec.LookPath("bash"); err == nil {
			cmd = exec.Command(bash, "-c", cmdline)
		} else {
			cmd = exec.Command("cmd", "/C", cmdline)
		}
	} else {
		cmd = exec.Command("sh", "-c", cmdline)
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = bytes.NewReader(input), stdout, io.Discard
	_ = cmd.Run()
}

func compactLine(raw []byte) string {
	type win struct {
		UsedPercentage *float64 `json:"used_percentage"`
		ResetsAt       *float64 `json:"resets_at"`
	}
	var p struct {
		ContextWindow *struct {
			UsedPercentage *float64 `json:"used_percentage"`
		} `json:"context_window"`
		RateLimits *struct {
			FiveHour *win `json:"five_hour"`
			SevenDay *win `json:"seven_day"`
		} `json:"rate_limits"`
	}
	_ = json.Unmarshal(raw, &p)
	pct := func(v *float64) string {
		if v == nil {
			return "—"
		}
		return fmt.Sprintf("%.0f%%", *v)
	}
	w := func(x *win) string {
		if x == nil || (x.ResetsAt != nil && int64(*x.ResetsAt) < time.Now().Unix()) {
			return "—"
		}
		return pct(x.UsedPercentage)
	}
	var ctx *float64
	if p.ContextWindow != nil {
		ctx = p.ContextWindow.UsedPercentage
	}
	var five, seven *win
	if p.RateLimits != nil {
		five, seven = p.RateLimits.FiveHour, p.RateLimits.SevenDay
	}
	return fmt.Sprintf("trk · ctx %s · 5h %s · 7d %s", pct(ctx), w(five), w(seven))
}
```

`internal/cli/serve.go`:
```go
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/GujaLomsadze/trk/internal/client"
	"github.com/GujaLomsadze/trk/internal/config"
	"github.com/GujaLomsadze/trk/internal/daemon"
)

func serve(stdout, stderr io.Writer) int {
	dir, err := config.DataDir()
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fmt.Fprintf(stderr, "trk serve: data dir: %v\n", err)
		return 1
	}
	logPath := filepath.Join(dir, "trk.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 5<<20 {
		os.Truncate(logPath, 0)
	}
	var logw io.Writer = io.Discard
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		defer f.Close()
		logw = f
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	addr := fmt.Sprintf("127.0.0.1:%d", config.Port())
	err = daemon.Run(ctx, daemon.Options{
		Addr: addr, DataDir: dir, Logger: log.New(logw, "", log.LstdFlags),
		OnListen: func(string) { fmt.Fprintf(stdout, "trk serving http://localhost:%d\n", config.Port()) },
	})
	if errors.Is(err, daemon.ErrAlreadyRunning) {
		fmt.Fprintf(stdout, "trk already running at http://localhost:%d\n", config.Port())
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "trk serve: %v\n", err)
		return 1
	}
	return 0
}

func open(stdout, stderr io.Writer) int {
	url := config.BaseURL() + "/"
	if !client.Default().EnsureDaemon(3 * time.Second) {
		fmt.Fprintf(stderr, "trk: daemon not reachable at %s\n", url)
		return 1
	}
	fmt.Fprintln(stdout, url)
	_ = openBrowser(url)
	return 0
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	}
	if b, err := os.ReadFile("/proc/version"); err == nil && strings.Contains(strings.ToLower(string(b)), "microsoft") {
		if p, err := exec.LookPath("wslview"); err == nil {
			return exec.Command(p, url).Start()
		}
		return exec.Command("cmd.exe", "/c", "start", "", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}
```

Modify `cli.Run` — replace the body after the `len(args)==0` check:
```go
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "start", "step", "progress", "blocked", "done", "hook", "statusline":
		return safe(func() int {
			switch cmd {
			case "hook":
				return hook(stdin)
			case "statusline":
				return statusline(rest, stdin, stdout)
			}
			return report(cmd, rest, stderr)
		})
	case "serve":
		return serve(stdout, stderr)
	case "open":
		return open(stdout, stderr)
	case "init":
		return initCmd(rest, stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "trk %s\n", version.Version)
		return 0
	case "help", "--help", "-h":
		usage(stdout)
		return 0
	}
	fmt.Fprintf(stderr, "trk: unknown command %q (try `trk help`)\n", cmd)
	return 2
}

// safe guarantees exit 0 for agent-facing commands, even on panic.
func safe(f func() int) (code int) {
	defer func() {
		if recover() != nil {
			code = 0
		}
	}()
	f()
	return 0
}
```
Until Task 10 lands, add a temporary `func initCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int { fmt.Fprintln(stderr, "trk init: not implemented yet"); return 1 }` in `init.go`; Task 10 replaces it.

- [ ] **Step 4: Run** — `go test ./internal/cli -v && go build -o trk ./cmd/trk && echo '{}' | ./trk hook; echo "exit=$?"` → PASS, `exit=0`.
- [ ] **Step 5: Manual smoke** — `TRK_PORT=17777 TRK_DATA_DIR=$(mktemp -d) ./trk step "hello"` then `curl -s localhost:17777/v1/sessions` shows a session with `step_text":"hello"` (auto-spawned). Kill: `kill $(cat $TRK_DATA_DIR/trk.pid)`.
- [ ] **Step 6: Commit** — `git add internal/cli && git commit -m "feat(cli): reporting verbs, hook, statusline, serve and open"`

---

### Task 10: claudecfg + `trk init`

**Files:**
- Create: `internal/claudecfg/ojson.go`, `internal/claudecfg/merge.go`, `internal/claudecfg/init.go`
- Modify: `internal/cli/init.go` (replace stub)
- Test: `internal/claudecfg/ojson_test.go`, `internal/claudecfg/merge_test.go`, `internal/claudecfg/init_test.go`

**Interfaces:**
- Produces:
  - `type Object` (ordered JSON object): `NewObject()`, `Get(k) (any, bool)`, `Set(k, v)`, `Keys() []string`, `Parse([]byte) (*Object, error)`, `(*Object) Marshal() ([]byte, error)` (2-space indent, trailing newline, no HTML escaping, numbers kept as `json.Number`).
  - `MergeHooks(root *Object, trkCmd string) (changed bool, err error)`
  - `type StatusAction string` with `StatusAdded|StatusPresent|StatusChained|StatusKept`; `MergeStatusLine(root *Object, trkCmd string, chain bool) (StatusAction, error)`; `ExistingStatusCommand(root *Object) string`
  - `BlockBegin`, `BlockEnd`, `Snippet` (spec §5.3 text verbatim), `ApplyBlock(existing string) string`
  - `ShellQuote(s string) string`
  - `type Paths struct{ Settings, ClaudeMD string }`, `DefaultPaths() (Paths, error)` (honours `CLAUDE_CONFIG_DIR`)
  - `type InitOptions struct{ Paths Paths; TrkCmd string; ChainStatus func(existing string) bool; SkipStatusLine, SkipClaudeMD, DryRun bool; Now time.Time }`
  - `type InitResult struct{ HooksChanged bool; Status StatusAction; ExistingStatus, SettingsBackup string; ClaudeMDChanged bool; ClaudeMDBackup string }`
  - `Init(InitOptions) (InitResult, error)`

Rules:
- Each hook entry: `{"hooks":[{"type":"command","command":"<trk> hook","async":true,"timeout":10}]}` (no matcher = all). Existing trk hook (regex `(^|[\s/\\"'])trk(\.exe)?["']?\s+hook(\s|$)`) → skipped; if its command differs from the current `<trk> hook`, update it in place.
- `hooks` not an object, or `hooks.<Event>` not an array → error, nothing written.
- Status line: absent → `{"type":"command","command":"<trk> statusline"}`; already trk → present; other command + chain → `<trk> statusline --then '<orig>'` keeping every other key (`padding`, `refreshInterval`); declined → kept.
- Backups: `<file>.trk-backup-YYYYMMDD-HHMMSS`, only when we're about to write and the file existed. Atomic write: temp file in the target dir + rename; resolve symlinks first (write the target, keep the link); preserve file mode.
- Invalid JSON → error, no write. Empty file → treated as `{}`.
- `trkCmd`: if `exec.LookPath("trk")` is the same file as `os.Executable()`, use that **absolute** LookPath path (stable across brew upgrades); else the executable path; forward slashes; `ShellQuote` when it contains spaces or quotes.
- CLI flags: `--yes` (chain without asking), `--no-statusline`, `--no-claude-md`, `--claude-md PATH`, `--dry-run`. Interactive prompt only when stdin is a TTY; otherwise keep the existing status line and print the `--yes` hint. After writing: start the daemon (`EnsureDaemon(3s)`) and print `Dashboard: http://localhost:7777` and "restart running Claude Code sessions to load the hooks".

- [ ] **Step 1: Failing tests**

`internal/claudecfg/ojson_test.go`:
```go
package claudecfg

import "testing"

func TestRoundTripPreservesOrderAndNumbers(t *testing.T) {
	in := `{
  "zeta": 1,
  "alpha": {
    "b": [1.50, true, null, "x<y>"],
    "a": {}
  },
  "big": 12345678901234567890,
  "empty": []
}
`
	o, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out, _ := o.Marshal()
	if string(out) != in {
		t.Fatalf("round trip changed file:\n%s", out)
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{`[1,2]`, `{"a":1} trailing`, `{"a":`, `"str"`} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("Parse(%q) accepted", in)
		}
	}
	o, err := Parse([]byte("  \n"))
	if err != nil || len(o.Keys()) != 0 {
		t.Fatalf("empty file: %v %v", o, err)
	}
}
```

`internal/claudecfg/merge_test.go`:
```go
package claudecfg

import (
	"strings"
	"testing"
)

const trk = "/home/u/.local/bin/trk"

func mustParse(t *testing.T, s string) *Object {
	t.Helper()
	o, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestMergeHooks(t *testing.T) {
	cases := []struct {
		name, in    string
		wantChanged bool
		wantErr     bool
		check       func(t *testing.T, out string)
	}{
		{"empty settings", `{}`, true, false, func(t *testing.T, out string) {
			if strings.Count(out, trk+" hook") != 11 || !strings.Contains(out, `"async": true`) {
				t.Fatalf("out = %s", out)
			}
		}},
		{"keeps user hooks and appends", `{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"caveman.sh"}]}]}}`, true, false, func(t *testing.T, out string) {
			if !strings.Contains(out, "caveman.sh") || strings.Index(out, "caveman.sh") > strings.Index(out, trk+" hook") {
				t.Fatalf("user hook lost or reordered: %s", out)
			}
		}},
		{"idempotent", "", false, false, nil}, // filled below from a merged result
		{"stale trk path updated", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/old/bin/trk hook"}]}]}}`, true, false, func(t *testing.T, out string) {
			if strings.Contains(out, "/old/bin/trk") || strings.Count(out, trk+" hook") != 11 {
				t.Fatalf("out = %s", out)
			}
		}},
		{"hooks not object", `{"hooks":[1]}`, false, true, nil},
		{"event not array", `{"hooks":{"Stop":{"x":1}}}`, false, true, nil},
		{"trkfoo is not trk", `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"trkfoo hook"}]}]}}`, true, false, func(t *testing.T, out string) {
			if !strings.Contains(out, "trkfoo hook") || strings.Count(out, trk+" hook") != 11 {
				t.Fatalf("out = %s", out)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			if c.name == "idempotent" {
				o := mustParse(t, `{}`)
				MergeHooks(o, trk)
				b, _ := o.Marshal()
				in = string(b)
			}
			o := mustParse(t, in)
			changed, err := MergeHooks(o, trk)
			if (err != nil) != c.wantErr || changed != c.wantChanged {
				t.Fatalf("changed=%v err=%v", changed, err)
			}
			if c.check != nil {
				b, _ := o.Marshal()
				c.check(t, string(b))
			}
		})
	}
}

func TestMergeStatusLine(t *testing.T) {
	cases := []struct {
		name, in string
		chain    bool
		want     StatusAction
		contains string
	}{
		{"absent", `{}`, false, StatusAdded, `"command": "` + trk + ` statusline"`},
		{"already trk", `{"statusLine":{"type":"command","command":"trk statusline --then 'x'"}}`, true, StatusPresent, "--then 'x'"},
		{"chain keeps other keys", `{"statusLine":{"type":"command","command":"ccstatusline","padding":0,"refreshInterval":10}}`, true, StatusChained, `"command": "` + trk + ` statusline --then 'ccstatusline'",` + "\n" + `    "padding": 0,` + "\n" + `    "refreshInterval": 10`},
		{"declined", `{"statusLine":{"type":"command","command":"ccstatusline"}}`, false, StatusKept, `"command": "ccstatusline"`},
		{"quotes escaped", `{"statusLine":{"type":"command","command":"jq -r '.model'"}}`, true, StatusChained, `--then 'jq -r '\\''.model'\\'''`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := mustParse(t, c.in)
			got, err := MergeStatusLine(o, trk, c.chain)
			b, _ := o.Marshal()
			if err != nil || got != c.want || !strings.Contains(string(b), c.contains) {
				t.Fatalf("action=%s err=%v out=%s", got, err, b)
			}
		})
	}
}

func TestApplyBlock(t *testing.T) {
	if got := ApplyBlock(""); got != Snippet {
		t.Fatalf("empty: %q", got)
	}
	user := "# My rules\n\nNever push.\n"
	once := ApplyBlock(user)
	if !strings.HasPrefix(once, user) || !strings.HasSuffix(once, Snippet) {
		t.Fatalf("append: %q", once)
	}
	if twice := ApplyBlock(once); twice != once {
		t.Fatal("not idempotent")
	}
	old := user + "\n" + BlockBegin + "\nold text\n" + BlockEnd + "\n\n# After\n"
	upd := ApplyBlock(old)
	if strings.Contains(upd, "old text") || !strings.Contains(upd, "# After") || strings.Count(upd, BlockBegin) != 1 {
		t.Fatalf("replace: %q", upd)
	}
}

func TestShellQuote(t *testing.T) {
	if got := ShellQuote(`it's`); got != `'it'\''s'` {
		t.Fatalf("got %s", got)
	}
}
```

`internal/claudecfg/init_test.go`:
```go
package claudecfg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func paths(t *testing.T) Paths {
	d := t.TempDir()
	return Paths{Settings: filepath.Join(d, "settings.json"), ClaudeMD: filepath.Join(d, "CLAUDE.md")}
}

func opts(p Paths, chain bool) InitOptions {
	return InitOptions{Paths: p, TrkCmd: trk, ChainStatus: func(string) bool { return chain }, Now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
}

func backups(t *testing.T, p string) []string {
	m, _ := filepath.Glob(p + ".trk-backup-*")
	return m
}

func TestInitFreshMachine(t *testing.T) {
	p := paths(t)
	res, err := Init(opts(p, true))
	if err != nil || !res.HooksChanged || res.Status != StatusAdded || !res.ClaudeMDChanged || res.SettingsBackup != "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	b, _ := os.ReadFile(p.Settings)
	md, _ := os.ReadFile(p.ClaudeMD)
	if !strings.Contains(string(b), trk+" hook") || string(md) != Snippet {
		t.Fatalf("settings=%s md=%q", b, md)
	}
}

func TestInitExistingThenIdempotent(t *testing.T) {
	p := paths(t)
	os.WriteFile(p.Settings, []byte(`{"model":"opus","statusLine":{"type":"command","command":"ccstatusline","refreshInterval":10}}`), 0o600)
	os.WriteFile(p.ClaudeMD, []byte("# Rules\n"), 0o644)
	res, err := Init(opts(p, true))
	if err != nil || res.Status != StatusChained || res.SettingsBackup == "" || res.ClaudeMDBackup == "" {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	orig, _ := os.ReadFile(res.SettingsBackup)
	if !strings.Contains(string(orig), `"ccstatusline"`) || strings.Contains(string(orig), "trk") {
		t.Fatal("backup is not the original")
	}
	if st, _ := os.Stat(p.Settings); runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("mode changed to %v", st.Mode().Perm())
	}
	b, _ := os.ReadFile(p.Settings)
	if strings.Index(string(b), `"model"`) > strings.Index(string(b), `"statusLine"`) {
		t.Fatal("key order changed")
	}
	n := len(backups(t, p.Settings))
	res2, err := Init(opts(p, true))
	if err != nil || res2.HooksChanged || res2.Status != StatusPresent || res2.ClaudeMDChanged || len(backups(t, p.Settings)) != n {
		t.Fatalf("second run not a no-op: %+v err=%v", res2, err)
	}
}

func TestInitInvalidJSONWritesNothing(t *testing.T) {
	p := paths(t)
	os.WriteFile(p.Settings, []byte(`{"hooks": {`), 0o644)
	if _, err := Init(opts(p, true)); err == nil {
		t.Fatal("accepted invalid settings.json")
	}
	b, _ := os.ReadFile(p.Settings)
	if string(b) != `{"hooks": {` || len(backups(t, p.Settings)) != 0 {
		t.Fatal("file touched")
	}
	if _, err := os.Stat(p.ClaudeMD); !os.IsNotExist(err) {
		t.Fatal("CLAUDE.md written despite settings error")
	}
}

func TestInitDryRun(t *testing.T) {
	p := paths(t)
	o := opts(p, true)
	o.DryRun = true
	res, err := Init(o)
	if err != nil || !res.HooksChanged {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, err := os.Stat(p.Settings); !os.IsNotExist(err) {
		t.Fatal("dry run wrote settings")
	}
}

func TestInitFollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges")
	}
	p := paths(t)
	target := filepath.Join(t.TempDir(), "dotfiles-settings.json")
	os.WriteFile(target, []byte(`{}`), 0o644)
	os.Symlink(target, p.Settings)
	if _, err := Init(opts(p, true)); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Lstat(p.Settings); fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink replaced by a file")
	}
	b, _ := os.ReadFile(target)
	if !strings.Contains(string(b), "trk") {
		t.Fatal("target not updated")
	}
}
```

- [ ] **Step 2: Run** — `go test ./internal/claudecfg` → FAIL.

- [ ] **Step 3: Implement**

`internal/claudecfg/ojson.go`:
```go
// Package claudecfg edits Claude Code's settings.json and CLAUDE.md safely.
package claudecfg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Object is a JSON object that keeps key order, so rewriting a user's file doesn't shuffle it.
type Object struct {
	keys []string
	vals map[string]any
}

func NewObject() *Object { return &Object{vals: map[string]any{}} }

func (o *Object) Get(k string) (any, bool) { v, ok := o.vals[k]; return v, ok }

func (o *Object) Set(k string, v any) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *Object) Keys() []string { return append([]string(nil), o.keys...) }

func Parse(data []byte) (*Object, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return NewObject(), nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the top-level object")
	}
	o, ok := v.(*Object)
	if !ok {
		return nil, errors.New("top level is not a JSON object")
	}
	return o, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return tok, nil // string, json.Number, bool, nil
	}
	switch d {
	case '{':
		o := NewObject()
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return nil, err
			}
			k, ok := kt.(string)
			if !ok {
				return nil, fmt.Errorf("object key %v is not a string", kt)
			}
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			o.Set(k, v)
		}
		_, err := dec.Token()
		return o, err
	case '[':
		arr := []any{}
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err := dec.Token()
		return arr, err
	}
	return nil, fmt.Errorf("unexpected %v", d)
}

func (o *Object) Marshal() ([]byte, error) {
	var b bytes.Buffer
	if err := writeValue(&b, o, ""); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func writeValue(b *bytes.Buffer, v any, indent string) error {
	switch t := v.(type) {
	case *Object:
		if len(t.keys) == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{\n")
		for i, k := range t.keys {
			b.WriteString(indent + "  ")
			if err := writeScalar(b, k); err != nil {
				return err
			}
			b.WriteString(": ")
			if err := writeValue(b, t.vals[k], indent+"  "); err != nil {
				return err
			}
			if i < len(t.keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "}")
	case []any:
		if len(t) == 0 {
			b.WriteString("[]")
			return nil
		}
		b.WriteString("[\n")
		for i, x := range t {
			b.WriteString(indent + "  ")
			if err := writeValue(b, x, indent+"  "); err != nil {
				return err
			}
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(indent + "]")
	default:
		return writeScalar(b, t)
	}
	return nil
}

func writeScalar(b *bytes.Buffer, v any) error {
	var tmp bytes.Buffer
	enc := json.NewEncoder(&tmp)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b.Write(bytes.TrimRight(tmp.Bytes(), "\n"))
	return nil
}
```
Note: the round-trip test writes arrays one element per line; if the expected fixture in `TestRoundTripPreservesOrderAndNumbers` disagrees with this formatting (it inlines `"b": [1.50, true, null, "x<y>"]`), change the fixture to the multi-line form — what matters is order, numbers (`1.50`, the 20-digit int) and `<>` survive verbatim.

`internal/claudecfg/merge.go`:
```go
package claudecfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/GujaLomsadze/trk/internal/model"
)

var (
	trkHookRe   = regexp.MustCompile(`(?:^|[\s/\\"'])trk(?:\.exe)?["']?\s+hook(?:\s|$)`)
	trkStatusRe = regexp.MustCompile(`(?:^|[\s/\\"'])trk(?:\.exe)?["']?\s+statusline(?:\s|$)`)
)

func MergeHooks(root *Object, trkCmd string) (bool, error) {
	var hooks *Object
	switch v, ok := root.Get("hooks"); {
	case !ok || v == nil:
		hooks = NewObject()
		root.Set("hooks", hooks)
	default:
		h, isObj := v.(*Object)
		if !isObj {
			return false, errors.New(`"hooks" in settings.json is not an object; refusing to edit`)
		}
		hooks = h
	}
	want := trkCmd + " hook"
	changed := false
	for _, ev := range model.HookEvents {
		var groups []any
		if v, ok := hooks.Get(ev); ok && v != nil {
			arr, isArr := v.([]any)
			if !isArr {
				return false, fmt.Errorf("hooks.%s in settings.json is not an array; refusing to edit", ev)
			}
			groups = arr
		}
		found, updated := updateTrkHook(groups, want)
		changed = changed || updated
		if found {
			continue
		}
		h := NewObject()
		h.Set("type", "command")
		h.Set("command", want)
		h.Set("async", true)
		h.Set("timeout", json.Number("10"))
		g := NewObject()
		g.Set("hooks", []any{h})
		hooks.Set(ev, append(groups, g))
		changed = true
	}
	return changed, nil
}

func updateTrkHook(groups []any, want string) (found, updated bool) {
	for _, g := range groups {
		gObj, ok := g.(*Object)
		if !ok {
			continue
		}
		hv, _ := gObj.Get("hooks")
		hs, _ := hv.([]any)
		for _, h := range hs {
			hObj, ok := h.(*Object)
			if !ok {
				continue
			}
			cv, _ := hObj.Get("command")
			cmd, _ := cv.(string)
			if !trkHookRe.MatchString(cmd) {
				continue
			}
			found = true
			if cmd != want {
				hObj.Set("command", want)
				updated = true
			}
		}
	}
	return found, updated
}

type StatusAction string

const (
	StatusAdded   StatusAction = "added"
	StatusPresent StatusAction = "present"
	StatusChained StatusAction = "chained"
	StatusKept    StatusAction = "kept"
)

func ExistingStatusCommand(root *Object) string {
	v, _ := root.Get("statusLine")
	sl, ok := v.(*Object)
	if !ok {
		return ""
	}
	cv, _ := sl.Get("command")
	cmd, _ := cv.(string)
	return cmd
}

func MergeStatusLine(root *Object, trkCmd string, chain bool) (StatusAction, error) {
	v, ok := root.Get("statusLine")
	if !ok || v == nil {
		sl := NewObject()
		sl.Set("type", "command")
		sl.Set("command", trkCmd+" statusline")
		root.Set("statusLine", sl)
		return StatusAdded, nil
	}
	sl, isObj := v.(*Object)
	if !isObj {
		return StatusKept, nil
	}
	cmd := ExistingStatusCommand(root)
	if trkStatusRe.MatchString(cmd) {
		return StatusPresent, nil
	}
	if cmd == "" || !chain {
		return StatusKept, nil
	}
	sl.Set("command", trkCmd+" statusline --then "+ShellQuote(cmd))
	return StatusChained, nil
}

const (
	BlockBegin = "<!-- trk:begin -->"
	BlockEnd   = "<!-- trk:end -->"
	Snippet    = BlockBegin + `
## Progress reporting
Report progress with the ` + "`trk`" + ` CLI (it never fails; don't check its output):
- At the start of a task: ` + "`trk start \"<task>\" --steps <N>`" + `
- When moving to a new step: ` + "`trk step \"<what you're doing>\"`" + ` and ` + "`trk progress <i>/<N>`" + `
- When you need a human decision: ` + "`trk blocked \"<question>\"`" + `
- When finished: ` + "`trk done \"<one-line result>\"`" + `
` + BlockEnd + "\n"
)

func ApplyBlock(existing string) string {
	b, e := strings.Index(existing, BlockBegin), strings.Index(existing, BlockEnd)
	if b >= 0 && e > b {
		return existing[:b] + strings.TrimSuffix(Snippet, "\n") + existing[e+len(BlockEnd):]
	}
	if strings.TrimSpace(existing) == "" {
		return Snippet
	}
	return strings.TrimRight(existing, "\n") + "\n\n" + Snippet
}

func ShellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
```
(`TestApplyBlock` expects `once` to start with `user` verbatim: `user` ends in exactly one `\n`, and `TrimRight + "\n\n"` gives `user + "\n" + Snippet`. Holds.)

`internal/claudecfg/init.go`:
```go
package claudecfg

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Paths struct{ Settings, ClaudeMD string }

func DefaultPaths() (Paths, error) {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Paths{}, err
		}
		dir = filepath.Join(home, ".claude")
	}
	return Paths{Settings: filepath.Join(dir, "settings.json"), ClaudeMD: filepath.Join(dir, "CLAUDE.md")}, nil
}

type InitOptions struct {
	Paths          Paths
	TrkCmd         string
	ChainStatus    func(existing string) bool
	SkipStatusLine bool
	SkipClaudeMD   bool
	DryRun         bool
	Now            time.Time
}

type InitResult struct {
	HooksChanged    bool
	Status          StatusAction
	ExistingStatus  string
	SettingsBackup  string
	ClaudeMDChanged bool
	ClaudeMDBackup  string
}

func Init(o InitOptions) (InitResult, error) {
	var res InitResult
	raw, err := os.ReadFile(o.Paths.Settings)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	existed := err == nil
	root, err := Parse(raw)
	if err != nil {
		return res, errors.New(o.Paths.Settings + ": " + err.Error() + " (nothing was changed)")
	}
	if res.HooksChanged, err = MergeHooks(root, o.TrkCmd); err != nil {
		return res, err
	}
	if !o.SkipStatusLine {
		res.ExistingStatus = ExistingStatusCommand(root)
		chain := false
		if res.ExistingStatus != "" && !trkStatusRe.MatchString(res.ExistingStatus) && o.ChainStatus != nil {
			chain = o.ChainStatus(res.ExistingStatus)
		}
		if res.Status, err = MergeStatusLine(root, o.TrkCmd, chain); err != nil {
			return res, err
		}
	}
	if (res.HooksChanged || res.Status == StatusAdded || res.Status == StatusChained) && !o.DryRun {
		out, err := root.Marshal()
		if err != nil {
			return res, err
		}
		if existed {
			if res.SettingsBackup, err = backup(o.Paths.Settings, raw, o.Now); err != nil {
				return res, err
			}
		}
		if err := writeAtomic(o.Paths.Settings, out); err != nil {
			return res, err
		}
	}
	if o.SkipClaudeMD {
		return res, nil
	}
	md, err := os.ReadFile(o.Paths.ClaudeMD)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return res, err
	}
	mdExisted := err == nil
	updated := ApplyBlock(string(md))
	res.ClaudeMDChanged = updated != string(md)
	if res.ClaudeMDChanged && !o.DryRun {
		if mdExisted {
			if res.ClaudeMDBackup, err = backup(o.Paths.ClaudeMD, md, o.Now); err != nil {
				return res, err
			}
		}
		if err := writeAtomic(o.Paths.ClaudeMD, []byte(updated)); err != nil {
			return res, err
		}
	}
	return res, nil
}

func backup(path string, data []byte, now time.Time) (string, error) {
	dst := path + ".trk-backup-" + now.Format("20060102-150405")
	return dst, os.WriteFile(dst, data, 0o600)
}

// writeAtomic writes via temp file + rename, following symlinks (dotfile setups) and keeping the mode.
func writeAtomic(path string, data []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := fs.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".trk-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

`internal/cli/init.go` (replaces stub):
```go
package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/claudecfg"
	"github.com/GujaLomsadze/trk/internal/client"
	"github.com/GujaLomsadze/trk/internal/config"
)

func initCmd(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(stderr)
	yes := fs.Bool("yes", false, "accept defaults without prompting (chains an existing status line)")
	noStatus := fs.Bool("no-statusline", false, "leave the status line alone")
	noMD := fs.Bool("no-claude-md", false, "don't add the CLAUDE.md block")
	mdPath := fs.String("claude-md", "", "CLAUDE.md to edit (default: <claude config dir>/CLAUDE.md)")
	dry := fs.Bool("dry-run", false, "show what would change, write nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths, err := claudecfg.DefaultPaths()
	if err != nil {
		fmt.Fprintf(stderr, "trk init: %v\n", err)
		return 1
	}
	if *mdPath != "" {
		paths.ClaudeMD = *mdPath
	}
	in := bufio.NewReader(stdin)
	chain := func(existing string) bool {
		if *yes {
			return true
		}
		if !isTTY(stdin) {
			fmt.Fprintf(stdout, "Kept your status line (%s). Re-run with --yes to chain it through trk.\n", existing)
			return false
		}
		fmt.Fprintf(stdout, "You already have a status line: %s\nChain it through trk? trk records the data, then shows your status line unchanged. [Y/n] ", existing)
		line, _ := in.ReadString('\n')
		a := strings.ToLower(strings.TrimSpace(line))
		return a == "" || a == "y" || a == "yes"
	}
	res, err := claudecfg.Init(claudecfg.InitOptions{
		Paths: paths, TrkCmd: trkCommand(), ChainStatus: chain,
		SkipStatusLine: *noStatus, SkipClaudeMD: *noMD, DryRun: *dry, Now: time.Now(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "trk init: %v\n", err)
		return 1
	}
	prefix := ""
	if *dry {
		prefix = "(dry run) would have: "
	}
	if res.HooksChanged {
		fmt.Fprintf(stdout, "%sadded trk hooks to %s\n", prefix, paths.Settings)
	} else {
		fmt.Fprintln(stdout, "hooks already set up")
	}
	switch res.Status {
	case claudecfg.StatusAdded:
		fmt.Fprintf(stdout, "%sset the status line to trk\n", prefix)
	case claudecfg.StatusChained:
		fmt.Fprintf(stdout, "%schained your status line through trk\n", prefix)
	case claudecfg.StatusPresent:
		fmt.Fprintln(stdout, "status line already goes through trk")
	}
	if res.SettingsBackup != "" {
		fmt.Fprintf(stdout, "backup: %s\n", res.SettingsBackup)
	}
	if res.ClaudeMDChanged {
		fmt.Fprintf(stdout, "%sadded the progress-reporting block to %s\n", prefix, paths.ClaudeMD)
	}
	if res.ClaudeMDBackup != "" {
		fmt.Fprintf(stdout, "backup: %s\n", res.ClaudeMDBackup)
	}
	if *dry {
		return 0
	}
	client.Default().EnsureDaemon(3 * time.Second)
	fmt.Fprintf(stdout, "\nDashboard: http://localhost:%d\nRestart any running Claude Code sessions so they load the hooks.\n", config.Port())
	return 0
}

func isTTY(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// trkCommand returns the command hooks should run: an absolute, stable path.
func trkCommand() string {
	exe, err := os.Executable()
	if err != nil {
		return "trk"
	}
	if lp, err := exec.LookPath("trk"); err == nil {
		if a, e1 := os.Stat(lp); e1 == nil {
			if b, e2 := os.Stat(exe); e2 == nil && os.SameFile(a, b) {
				if abs, err := filepath.Abs(lp); err == nil {
					exe = abs
				}
			}
		}
	}
	exe = filepath.ToSlash(exe)
	if strings.ContainsAny(exe, " '\"") {
		return claudecfg.ShellQuote(exe)
	}
	return exe
}
```

- [ ] **Step 4: Run** — `go test ./internal/claudecfg ./internal/cli -v` → PASS.
- [ ] **Step 5: Sandbox check (never touch the real `~/.claude` here)** — `CLAUDE_CONFIG_DIR=$(mktemp -d) TRK_PORT=17777 TRK_DATA_DIR=$(mktemp -d) ./trk init --yes` → prints the summary; inspect the generated settings.json.
- [ ] **Step 6: Commit** — `git add internal/claudecfg internal/cli && git commit -m "feat(init): safe settings.json merge, status line chaining, CLAUDE.md block"`

---

### Task 11: Dashboard — phosphor Fleet view

**Files:**
- Create: `scripts/fetch-fonts.sh`, `web/fonts/` (IBMPlexMono-Regular.ttf, IBMPlexMono-SemiBold.ttf, VT323-Regular.ttf, OFL-IBMPlexMono.txt, OFL-VT323.txt), `web/theme.css`, `web/app.js`, `scripts/seed-demo.sh`
- Modify: `web/index.html` (full page), `web/embed.go` (pattern `index.html app.js theme.css fonts`)
- Test: `web/web_test.go`

**Interfaces:**
- Consumes: SSE `snapshot` = `fleet.View` JSON (fields: `now, sessions[], needs[], collisions[], account, stats`; session fields from `model.Session` + `loop, reality, recent[{tool,summary,state}], timeline[{start,end,cat}], attention, need, ctx{level,hints[]}`).
- Produces: embedded UI.

Layout per spec §7.1: header (title `TRK.EXE`, subtitle `fleet monitor · localhost:7777`, stats, 5h/7d ASCII bars with reset countdowns, nav Fleet/Timeline/Orbit with the last two disabled) → main grid (cards, ~2/3) + right column (Needs you, Collisions) → bottom "Last 30 minutes" swimlane (1800 px for 30 min, horizontal scroll, lane label sticky, ticks every 5 min, scrolled to "now" on first render).

Card: name + chip; `repo · branch`; task (or muted "no task declared"); `step i/N · text`; 20-cell ASCII bar toned by status; reality box (amber) when `reality`; last 3 tools `› Tool summary ✓/✗/…`; **context gauge** `ctx 42% of 200K` + 10-cell bar, coloured by `ctx.level` (`ok` muted, `yellow` warn, `red` accent with glow), and each `ctx.hints[]` line below it prefixed `▲` in the band colour; footer elapsed · tokens · cost. `attention` → hot border.

Rules: build DOM only with `document.createElement` + `textContent` (helper `h()`); no `innerHTML` anywhere. Re-render on every snapshot and once per second (ages/countdowns from `view.now` + local clock skew). Connection pill: "● live" / "○ reconnecting" (EventSource retries on its own). Touch targets ≥44 px. Under 900 px the right column stacks below the grid.

- [ ] **Step 1: Fonts** — create `scripts/fetch-fonts.sh`:
```sh
#!/usr/bin/env sh
# Downloads the OFL fonts embedded in the dashboard. Run once; commit the results.
set -eu
cd "$(dirname "$0")/../web/fonts"
base=https://raw.githubusercontent.com/google/fonts/main/ofl
curl -fsSLo VT323-Regular.ttf        "$base/vt323/VT323-Regular.ttf"
curl -fsSLo OFL-VT323.txt            "$base/vt323/OFL.txt"
curl -fsSLo IBMPlexMono-Regular.ttf  "$base/ibmplexmono/IBMPlexMono-Regular.ttf"
curl -fsSLo IBMPlexMono-SemiBold.ttf "$base/ibmplexmono/IBMPlexMono-SemiBold.ttf"
curl -fsSLo OFL-IBMPlexMono.txt      "$base/ibmplexmono/OFL.txt"
ls -la
```
Run: `mkdir -p web/fonts && sh scripts/fetch-fonts.sh` → five files, each TTF >50 KB. If a URL 404s, look the path up at github.com/google/fonts/tree/main/ofl and fix it.

- [ ] **Step 2: Failing test** `web/web_test.go`:
```go
package web

import (
	"io/fs"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestAssetsEmbedded(t *testing.T) {
	for _, p := range []string{"index.html", "app.js", "theme.css", "fonts/VT323-Regular.ttf", "fonts/IBMPlexMono-Regular.ttf", "fonts/IBMPlexMono-SemiBold.ttf"} {
		if _, err := fs.Stat(FS, p); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	idx, _ := fs.ReadFile(FS, "index.html")
	for _, ref := range []string{"theme.css", "app.js", "TRK.EXE"} {
		if !strings.Contains(string(idx), ref) {
			t.Errorf("index.html lacks %s", ref)
		}
	}
}

// Agent-supplied text must never be parsed as HTML.
func TestNoInnerHTML(t *testing.T) {
	js, _ := os.ReadFile("app.js")
	if regexp.MustCompile(`innerHTML|outerHTML|insertAdjacentHTML|document\.write`).Match(js) {
		t.Fatal("app.js uses an HTML-parsing sink")
	}
}

func TestThemeTokens(t *testing.T) {
	css, _ := os.ReadFile("theme.css")
	for _, tok := range []string{"--bg: #0B0605", "--panel: #110908", "--border: #3A1A16", "--text: #F1DCD6", "--muted: #B9928A", "--accent: #FF5A4A", "--warn: #FFB27A", "--hot: #6E2A20"} {
		if !strings.Contains(string(css), tok) {
			t.Errorf("theme.css missing token %q", tok)
		}
	}
}
```

- [ ] **Step 3: Run** — `go test ./web` → FAIL (missing app.js/theme.css).

- [ ] **Step 4: Implement** — `web/embed.go` pattern becomes `//go:embed index.html app.js theme.css fonts`.

`web/index.html`:
```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>TRK.EXE</title>
<link rel="stylesheet" href="theme.css">
</head>
<body>
<header class="hdr panel">
  <div class="brand">
    <h1 class="title">TRK.EXE</h1>
    <div class="sub">fleet monitor · <span id="host"></span> <span id="conn" class="conn">○ connecting</span></div>
  </div>
  <div class="stats" id="stats"></div>
  <div class="limits" id="limits"></div>
  <nav class="nav" aria-label="views">
    <a class="btn btn-secondary active" href="#">Fleet</a>
    <span class="btn btn-secondary disabled" title="coming later">Timeline</span>
    <span class="btn btn-secondary disabled" title="coming later">Orbit</span>
  </nav>
</header>
<main class="layout">
  <section class="grid" id="grid" aria-label="agents"></section>
  <aside class="side">
    <section class="panel box"><h2>Needs you</h2><ul class="list" id="needs"></ul></section>
    <section class="panel box"><h2>Collisions</h2><ul class="list" id="collisions"></ul></section>
  </aside>
</main>
<section class="panel box timeline">
  <h2>Last 30 minutes</h2>
  <div class="tl-scroll" id="tl-scroll"><div id="timeline"></div></div>
  <div class="tl-legend"><span class="tl-key tl-read"></span>read <span class="tl-key tl-edit"></span>edit <span class="tl-key tl-run"></span>run <span class="tl-key tl-wait"></span>waiting on you</div>
</section>
<div class="scanlines" aria-hidden="true"></div>
<script src="app.js"></script>
</body>
</html>
```

`web/theme.css`:
```css
@font-face { font-family: "IBM Plex Mono"; src: url("fonts/IBMPlexMono-Regular.ttf") format("truetype"); font-weight: 400; font-display: swap; }
@font-face { font-family: "IBM Plex Mono"; src: url("fonts/IBMPlexMono-SemiBold.ttf") format("truetype"); font-weight: 600; font-display: swap; }
@font-face { font-family: "VT323"; src: url("fonts/VT323-Regular.ttf") format("truetype"); font-weight: 400; font-display: swap; }

/* Phosphor theme — spec §7.2. Other themes later = another token set. */
:root {
  --bg: #0B0605;
  --panel: #110908;
  --border: #3A1A16;
  --rule-soft: #2A1411;
  --text: #F1DCD6;
  --muted: #B9928A;
  --accent: #FF5A4A;
  --accent-fg: #0B0605;
  --warn: #FFB27A;
  --hot: #6E2A20;
  --done: #7A5650;
  --track: #2A1411;
  --chip-accent-bg: rgba(255,90,74,0.10);
  --chip-accent-fg: #FF7A6B;
  --chip-warn-bg: rgba(255,178,122,0.10);
  --chip-warn-fg: #FFB27A;
  --chip-done-fg: #B9928A;
  --tl-read: #3A1A16;
  --tl-edit: #FF5A4A;
  --tl-run: #F1DCD6;
  --tl-wait: #FFB27A;
  --glow-title: 0 0 10px rgba(255,90,74,0.7);
  --glow: 0 0 6px rgba(255,90,74,0.55);
  --font-read: "IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  --font-deco: "VT323", "IBM Plex Mono", ui-monospace, monospace;
  --radius: 2px;
  color-scheme: dark;
}

* { box-sizing: border-box; }
html, body { margin: 0; background: var(--bg); color: var(--text); font: 14px/1.5 var(--font-read); }
body { padding: 16px; min-height: 100vh; }
h2 { font: 600 16px/1.3 var(--font-read); text-transform: uppercase; letter-spacing: 0.04em; margin: 0 0 12px; }
ul { list-style: none; margin: 0; padding: 0; }
.muted, .meta { color: var(--muted); font-size: 12px; }

.panel { background: var(--panel); border: 1px solid var(--border); border-radius: var(--radius); box-shadow: inset 0 1px 0 rgba(255,90,74,0.18); }
.box { padding: 16px; }

/* header */
.hdr { display: flex; flex-wrap: wrap; align-items: center; gap: 16px 32px; padding: 12px 16px; margin-bottom: 16px; }
.title { font: 36px/1 var(--font-deco); letter-spacing: 0.08em; color: var(--accent); text-shadow: var(--glow-title); margin: 0; }
.sub { color: var(--muted); font-size: 12px; }
.conn { margin-left: 8px; }
.conn.live { color: var(--chip-accent-fg); }
.stats { display: flex; gap: 20px; flex-wrap: wrap; }
.stat b { display: block; font: 19px/1 var(--font-deco); color: var(--text); text-transform: uppercase; }
.stat span { color: var(--muted); font-size: 12px; }
.stat.hot b { color: var(--warn); }
.limits { display: grid; gap: 2px; }
.limit { display: flex; gap: 8px; align-items: baseline; white-space: nowrap; }
.limit-label { color: var(--muted); font-size: 12px; width: 2.2em; }
.nav { display: flex; gap: 8px; margin-left: auto; }
.btn { display: inline-flex; align-items: center; min-height: 44px; padding: 0 14px; border-radius: var(--radius);
  font: 19px/1 var(--font-deco); text-transform: uppercase; text-decoration: none; }
.btn-primary { background: var(--accent); color: var(--accent-fg); border: 1px solid var(--accent); }
.btn-secondary { background: transparent; color: var(--chip-accent-fg); border: 1px solid var(--border); text-shadow: var(--glow); }
.btn.active { border-color: var(--accent); }
.btn.disabled { opacity: 0.45; cursor: default; }

/* layout */
.layout { display: grid; grid-template-columns: minmax(0, 2fr) minmax(280px, 1fr); gap: 16px; margin-bottom: 16px; align-items: start; }
.grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(320px, 1fr)); gap: 16px; }
.side { display: grid; gap: 16px; }
.empty { color: var(--muted); padding: 24px; text-align: center; }
@media (max-width: 900px) { .layout { grid-template-columns: 1fr; } .nav { margin-left: 0; } }

/* card */
.card { padding: 14px 16px; display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.card.attention { border-color: var(--hot); }
.card-head { display: flex; justify-content: space-between; gap: 12px; align-items: flex-start; }
.card-name { font: 600 16px/1.3 var(--font-read); margin: 0; overflow-wrap: anywhere; }
.task { margin: 0; overflow-wrap: anywhere; }
.step { color: var(--muted); overflow-wrap: anywhere; }
.chip { font: 19px/1 var(--font-deco); text-transform: uppercase; padding: 4px 8px; border-radius: 0; white-space: nowrap; text-shadow: var(--glow); }
.chip-accent { background: var(--chip-accent-bg); color: var(--chip-accent-fg); }
.chip-warn { background: var(--chip-warn-bg); color: var(--chip-warn-fg); }
.chip-done { background: transparent; color: var(--chip-done-fg); border: 1px solid var(--rule-soft); text-shadow: none; }
.ascii { font: 19px/1 var(--font-deco); text-shadow: var(--glow); white-space: pre; }
.tone-accent { color: var(--accent); }
.tone-warn { color: var(--warn); }
.tone-done { color: var(--done); text-shadow: none; }
.reality { border: 1px solid var(--warn); color: var(--warn); padding: 6px 10px; border-radius: var(--radius); font-size: 12px; overflow-wrap: anywhere; }
.reality-label { font: 16px/1 var(--font-deco); text-transform: uppercase; margin-right: 8px; }
.tools { border-top: 1px solid var(--rule-soft); padding-top: 6px; font-size: 12px; }
.tool { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--muted); }
.tool-name { color: var(--text); }
.tool-fail .mark { color: var(--accent); }
.tool-ok .mark { color: var(--chip-accent-fg); }

/* context gauge + hints (user thresholds: 40 yellow, 60 red, hints at 50/70) */
.ctx { border-top: 1px solid var(--rule-soft); padding-top: 6px; }
.ctx-line { display: flex; justify-content: space-between; align-items: baseline; gap: 8px; font-size: 12px; }
.ctx-hint { font-size: 12px; margin-top: 2px; }
.ctx-ok .ctx-bar, .ctx-none .ctx-bar { color: var(--muted); text-shadow: none; }
.ctx-yellow .ctx-line, .ctx-yellow .ctx-bar, .ctx-yellow .ctx-hint { color: var(--warn); }
.ctx-red .ctx-line, .ctx-red .ctx-bar, .ctx-red .ctx-hint { color: var(--accent); }
.ctx-red .ctx-hint { font-weight: 600; }

.card-foot { display: flex; gap: 16px; color: var(--muted); font-size: 12px; border-top: 1px solid var(--rule-soft); padding-top: 6px; flex-wrap: wrap; }

/* inbox + collisions */
.list li { border-top: 1px solid var(--rule-soft); padding: 8px 0; }
.list li:first-child { border-top: 0; padding-top: 0; }
.need-head { font-size: 12px; color: var(--warn); }
.need-text { overflow-wrap: anywhere; }

/* timeline */
.tl-scroll { overflow-x: auto; }
.tl-axis, .tl-lane { display: flex; }
.tl-label { position: sticky; left: 0; z-index: 1; width: 140px; flex: none; background: var(--panel); color: var(--muted); font-size: 12px;
  padding-right: 8px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; line-height: 20px; }
.tl-track { position: relative; height: 20px; flex: none; border-bottom: 1px solid var(--rule-soft); }
.tl-axis .tl-track { border: 0; }
.tl-tick { position: absolute; top: 0; transform: translateX(-50%); color: var(--muted); font-size: 12px; }
.tl-block { position: absolute; top: 4px; height: 12px; border-radius: 1px; }
.tl-read { background: var(--tl-read); }
.tl-edit { background: var(--tl-edit); }
.tl-run { background: var(--tl-run); }
.tl-wait { background: var(--tl-wait); }
.tl-legend { margin-top: 8px; color: var(--muted); font-size: 12px; }
.tl-key { display: inline-block; width: 10px; height: 10px; margin: 0 4px 0 12px; vertical-align: middle; }
.tl-key:first-child { margin-left: 0; }

.scanlines { position: fixed; inset: 0; pointer-events: none; z-index: 10;
  background: repeating-linear-gradient(to bottom, transparent 0 2px, rgba(0,0,0,0.16) 3px, transparent 4px); }
```

`web/app.js`:
```js
"use strict";
// TRK dashboard. Builds DOM with createElement/textContent only: agent text is untrusted.

let view = null;
let skew = 0; // server clock - local clock
let firstTimeline = true;
const DASH = "—";
const $ = (id) => document.getElementById(id);
const now = () => Date.now() + skew;

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else el.setAttribute(k, v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

function bar(frac, cells = 20) {
  const f = Math.max(0, Math.min(1, frac || 0));
  const n = Math.round(f * cells);
  return "[" + "█".repeat(n) + "░".repeat(cells - n) + "]";
}
function dur(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s < 60) return s + "s";
  const m = Math.floor(s / 60);
  if (m < 60) return m + "m";
  const hr = Math.floor(m / 60);
  if (hr < 48) return hr + "h" + String(m % 60).padStart(2, "0") + "m";
  return Math.floor(hr / 24) + "d";
}
function tokens(n) {
  if (!n) return "0";
  if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
  if (n >= 1e3) return Math.round(n / 1e3) + "K";
  return String(n);
}
const pct = (v) => (v == null ? DASH : Math.round(v) + "%");
const money = (v) => "$" + (v || 0).toFixed(2);

const CHIP = {
  working: ["WORKING", "accent"], waiting: ["WAITING ON YOU", "warn"], blocked: ["BLOCKED", "warn"],
  looping: ["LOOPING?", "warn"], idle: ["IDLE", "done"], done: ["DONE", "done"],
};
const TONE = { working: "accent", waiting: "warn", blocked: "warn", looping: "warn", idle: "done", done: "done" };
const NEED_LABEL = { permission: "permission", question: "question", blocked: "blocked" };

function renderHeader(v) {
  $("host").textContent = location.host;
  const st = v.stats;
  $("stats").replaceChildren(
    h("div", { class: "stat" }, h("b", null, st.agents), h("span", null, "agents")),
    h("div", { class: "stat" }, h("b", null, st.working), h("span", null, "working")),
    h("div", { class: "stat" + (st.needs_you ? " hot" : "") }, h("b", null, st.needs_you), h("span", null, "needs you")),
    h("div", { class: "stat" }, h("b", null, tokens(st.tokens_today)), h("span", null, "tokens today")),
  );
  const a = v.account || {};
  $("limits").replaceChildren(limit("5h", a.five_h_pct, a.five_h_reset), limit("7d", a.seven_d_pct, a.seven_d_reset));
}

function limit(label, p, reset) {
  const live = p != null && (!reset || reset > now());
  return h("div", { class: "limit" },
    h("span", { class: "limit-label" }, label),
    h("span", { class: "ascii " + (live && p >= 80 ? "tone-warn" : "tone-accent") }, bar(live ? p / 100 : 0)),
    h("span", null, live ? Math.round(p) + "%" : DASH),
    h("span", { class: "meta" }, live && reset ? "resets " + dur(reset - now()) : ""));
}

function ctxGauge(s) {
  const c = s.ctx || { level: "none" };
  const size = s.ctx_size ? " of " + tokens(s.ctx_size) : "";
  return h("div", { class: "ctx ctx-" + c.level },
    h("div", { class: "ctx-line" },
      h("span", null, "ctx " + pct(s.ctx_pct) + size),
      h("span", { class: "ascii ctx-bar" }, bar((s.ctx_pct || 0) / 100, 10))),
    (c.hints || []).map((t) => h("div", { class: "ctx-hint" }, "▲ " + t)));
}

function card(s) {
  const [label, tone] = CHIP[s.status] || CHIP.working;
  const n = s.step_n || 0, i = s.step_i || 0;
  const where = [s.repo, s.branch].filter(Boolean).join(" · ") || s.cwd || "";
  const mark = (st) => (st === "ok" ? "✓" : st === "fail" ? "✗" : "…");
  return h("article", { class: "panel card" + (s.attention ? " attention" : "") },
    h("div", { class: "card-head" },
      h("div", null, h("h3", { class: "card-name" }, s.name), h("div", { class: "meta" }, where)),
      h("span", { class: "chip chip-" + tone }, label)),
    s.task ? h("p", { class: "task" }, s.task) : h("p", { class: "task muted" }, "no task declared"),
    n || s.step_text ? h("div", { class: "step" }, n ? `step ${i}/${n}` : "step", s.step_text ? " · " + s.step_text : "") : null,
    n ? h("div", { class: "ascii tone-" + (TONE[s.status] || "accent") }, bar(i / n)) : null,
    s.reality ? h("div", { class: "reality" }, h("span", { class: "reality-label" }, "reality"), s.reality) : null,
    s.recent && s.recent.length
      ? h("ul", { class: "tools" }, s.recent.map((c) =>
          h("li", { class: "tool tool-" + c.state, title: c.tool + " " + c.summary },
            "› ", h("span", { class: "tool-name" }, c.tool), " ", c.summary, " ", h("span", { class: "mark" }, mark(c.state)))))
      : null,
    ctxGauge(s),
    h("footer", { class: "card-foot" },
      h("span", null, "elapsed " + dur(now() - s.started_at)),
      h("span", null, tokens((s.tokens_in || 0) + (s.tokens_out || 0)) + " tok"),
      h("span", null, money(s.cost_usd))));
}

function renderGrid(v) {
  $("grid").replaceChildren(...(v.sessions.length
    ? v.sessions.map(card)
    : [h("div", { class: "panel empty" }, "No agents yet. Start Claude Code (after `trk init`) or run `trk start \"task\"`.")]));
}

function renderSide(v) {
  $("needs").replaceChildren(...(v.needs.length
    ? v.needs.map((n) => h("li", null,
        h("div", { class: "need-head" }, n.name, " · ", NEED_LABEL[n.type] || n.type, " · ", dur(now() - n.ts)),
        h("div", { class: "need-text" }, n.text || DASH)))
    : [h("li", { class: "muted" }, "nothing pending")]));
  $("collisions").replaceChildren(...(v.collisions.length
    ? v.collisions.map((c) => h("li", null,
        h("div", { class: "need-text" }, c.path),
        h("div", { class: "meta" }, c.names.join(" ↔ "), " · ", dur(now() - c.last_ts), " ago")))
    : [h("li", { class: "muted" }, "none")]));
}

function renderTimeline(v) {
  const W = 1800, end = now(), start = end - 30 * 60 * 1000;
  const x = (t) => ((Math.max(start, Math.min(end, t)) - start) / (end - start)) * W;
  const axis = h("div", { class: "tl-track", style: `width:${W}px` });
  for (let m = 30; m >= 0; m -= 5) axis.append(h("span", { class: "tl-tick", style: `left:${x(end - m * 60000)}px` }, m ? `-${m}m` : "now"));
  const lanes = v.sessions.filter((s) => s.timeline && s.timeline.length).map((s) => {
    const track = h("div", { class: "tl-track", style: `width:${W}px` });
    for (const b of s.timeline) {
      const l = x(b.start), r = x(b.end || end);
      track.append(h("span", { class: "tl-block tl-" + b.cat, style: `left:${l}px;width:${Math.max(2, r - l)}px`,
        title: `${b.cat} · ${dur((b.end || end) - b.start)}` }));
    }
    return h("div", { class: "tl-lane" }, h("div", { class: "tl-label", title: s.name }, s.name), track);
  });
  $("timeline").replaceChildren(
    h("div", { class: "tl-axis" }, h("div", { class: "tl-label" }, ""), axis),
    ...(lanes.length ? lanes : [h("div", { class: "muted" }, "no activity in the last 30 minutes")]));
  if (firstTimeline && lanes.length) {
    $("tl-scroll").scrollLeft = $("tl-scroll").scrollWidth;
    firstTimeline = false;
  }
}

function render() {
  if (!view) return;
  renderHeader(view);
  renderGrid(view);
  renderSide(view);
  renderTimeline(view);
}

function setConn(live) {
  const el = $("conn");
  el.textContent = live ? "● live" : "○ reconnecting";
  el.classList.toggle("live", live);
}

function connect() {
  const es = new EventSource("/v1/stream");
  es.addEventListener("snapshot", (e) => {
    view = JSON.parse(e.data);
    skew = view.now - Date.now();
    setConn(true);
    render();
  });
  es.onerror = () => setConn(false); // EventSource reconnects by itself
}

connect();
setInterval(render, 1000);
```

`scripts/seed-demo.sh` (demo data for screenshots; posts straight to the API):
```sh
#!/usr/bin/env sh
# Seed a running daemon with a believable fleet. Usage: TRK_PORT=17777 sh scripts/seed-demo.sh
set -eu
U="http://127.0.0.1:${TRK_PORT:-7777}/v1/events"
p() { curl -fsS -o /dev/null -H 'Content-Type: application/json' -d "$1" "$U"; }
hook() { p "{\"source\":\"hook\",\"payload\":{\"session_id\":\"$1\",\"cwd\":\"$2\",\"hook_event_name\":\"$3\"$4}}"; }
cli() { p "{\"source\":\"cli\",\"kind\":\"$2\",\"session_id\":\"$1\",\"session_via\":\"env\",\"payload\":$3}"; }
st() { p "{\"source\":\"statusline\",\"payload\":{\"session_id\":\"$1\",\"model\":{\"display_name\":\"Opus 5.5\"},\"cost\":{\"total_cost_usd\":$2},\"context_window\":{\"used_percentage\":$3,\"context_window_size\":200000,\"total_input_tokens\":$4,\"total_output_tokens\":4200},\"rate_limits\":{\"five_hour\":{\"used_percentage\":23,\"resets_at\":$(( $(date +%s) + 8000 ))},\"seven_day\":{\"used_percentage\":61,\"resets_at\":$(( $(date +%s) + 300000 ))}}}}"; }

hook kafka /src/kafka-consumer SessionStart ''
cli kafka start '{"text":"Refactor Kafka consumer","steps":5}'
cli kafka progress '{"i":3,"n":5}'
cli kafka step '{"text":"Writing tests"}'
for i in 1 2 3 4 5 6; do hook kafka /src/kafka-consumer PostToolUseFailure ",\"tool_name\":\"Bash\",\"tool_use_id\":\"f$i\",\"tool_input\":{\"command\":\"pytest tests/test_consumer.py -x\"}"; done
st kafka 3.12 54 108000

hook api /src/api-gateway SessionStart ''
cli api start '{"text":"Add rate limiting to /v2","steps":4}'
cli api progress '{"i":1,"n":4}'
hook api /src/api-gateway PreToolUse ',"tool_name":"Edit","tool_use_id":"e1","tool_input":{"file_path":"/src/shared/limits.go"}'
hook api /src/api-gateway PermissionRequest ',"tool_name":"Bash","tool_use_id":"b1","tool_input":{"command":"terraform apply -auto-approve"}'
st api 0.84 72 144000

hook docs /src/docs SessionStart ''
cli docs start '{"text":"Update migration guide","steps":3}'
hook docs /src/docs PreToolUse ',"tool_name":"Read","tool_use_id":"r1","tool_input":{"file_path":"/src/shared/limits.go"}'
hook docs /src/docs PostToolUse ',"tool_name":"Read","tool_use_id":"r1","tool_input":{"file_path":"/src/shared/limits.go"}'
cli docs blocked '{"text":"Drop or keep the legacy topic during cutover?"}'
st docs 0.31 18 36000
echo seeded
```

- [ ] **Step 5: Run** — `go test ./web ./internal/daemon && (command -v node >/dev/null && node --check web/app.js)` → PASS.
- [ ] **Step 6: Visual check** — `go build -o trk ./cmd/trk && TRK_PORT=17777 TRK_DATA_DIR=$(mktemp -d) ./trk serve &` then `TRK_PORT=17777 sh scripts/seed-demo.sh`; open `http://localhost:17777` with Playwright at 1440×900 and 400×800, take screenshots, and check: kafka card shows LOOPING? + reality "Bash `pytest …` failed 6×" + yellow ctx with the handoff hint; api card shows WAITING ON YOU, red ctx with **both** hints, and an inbox item `terraform apply -auto-approve`; docs is BLOCKED; collisions lists `/src/shared/limits.go` api ↔ docs; 5h 23% / 7d 61% bars with countdowns; no horizontal page scroll at 400 px; scanlines visible; no console errors. Fix anything off, then kill the daemon.
- [ ] **Step 7: Commit** — `git add web scripts && git commit -m "feat(web): phosphor fleet dashboard with context bands"`

---

### Task 12: End-to-end acceptance test

**Files:**
- Create: `e2e/e2e_test.go` (`//go:build e2e && (linux || darwin)`)

**Interfaces:**
- Consumes: the built `trk` binary only (black box).

Covers spec §11 locally: auto-spawn without `trk serve`; permission → WAITING ON YOU within 1 s; two concurrent fake-Claude sessions get correct CLI attribution via the process tree; killing the daemon mid-session → calls still exit 0 fast, next call respawns; status line with null fields prints dashes.

- [ ] **Step 1: Write the test**

```go
//go:build e2e && (linux || darwin)

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type harness struct {
	t                 *testing.T
	bin, data, port   string
	env               []string
}

func setup(t *testing.T) *harness {
	dir := t.TempDir()
	bin := filepath.Join(dir, "trk")
	if out, err := exec.Command("go", "build", "-o", bin, "../cmd/trk").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	l.Close()
	h := &harness{t: t, bin: bin, data: filepath.Join(dir, "data"), port: port}
	h.env = append(os.Environ(), "TRK_PORT="+port, "TRK_DATA_DIR="+h.data, "TRK_URL=", "TRK_SESSION=", "TRK_NO_SPAWN=")
	t.Cleanup(h.killDaemon)
	return h
}

func (h *harness) run(stdin string, args ...string) (int, string, time.Duration) {
	cmd := exec.Command(h.bin, args...)
	cmd.Env, cmd.Stdin = h.env, strings.NewReader(stdin)
	start := time.Now()
	out, _ := cmd.Output()
	return cmd.ProcessState.ExitCode(), string(out), time.Since(start)
}

// underClaude runs a shell script whose parent process is named "claude".
func (h *harness) underClaude(name, script string) {
	dir := filepath.Join(filepath.Dir(h.bin), name)
	os.MkdirAll(dir, 0o755)
	fake := filepath.Join(dir, "claude")
	src, _ := os.ReadFile("/bin/sh")
	os.WriteFile(fake, src, 0o755)
	cmd := exec.Command(fake, "-c", script+"; true")
	cmd.Env, cmd.Dir = h.env, dir
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("fake claude %s: %v %s", name, err, out)
	}
}

func (h *harness) view() map[string]any {
	resp, err := http.Get("http://127.0.0.1:" + h.port + "/v1/sessions")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var v map[string]any
	json.NewDecoder(resp.Body).Decode(&v)
	return v
}

func (h *harness) session(id string) map[string]any {
	v := h.view()
	if v == nil {
		return nil
	}
	for _, s := range v["sessions"].([]any) {
		if m := s.(map[string]any); m["session_id"] == id {
			return m
		}
	}
	return nil
}

func (h *harness) waitFor(what string, within time.Duration, ok func() bool) {
	h.t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	h.t.Fatalf("timed out after %v waiting for %s", within, what)
}

func (h *harness) killDaemon() {
	b, err := os.ReadFile(filepath.Join(h.data, "trk.pid"))
	if err != nil {
		return
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	syscall.Kill(pid, syscall.SIGTERM)
	h.waitFor("daemon exit", 5*time.Second, func() bool { return syscall.Kill(pid, 0) != nil })
}

func hookJSON(sid, event, cwd, extra string) string {
	return fmt.Sprintf(`{"session_id":%q,"hook_event_name":%q,"cwd":%q%s}`, sid, event, cwd, extra)
}

func TestAcceptance(t *testing.T) {
	h := setup(t)

	// 1. Auto-spawn: no `trk serve`, a hook call brings the daemon up.
	code, out, _ := h.run(hookJSON("A", "SessionStart", "/w/a", ""), "hook")
	if code != 0 || out != "" {
		t.Fatalf("hook: code=%d out=%q", code, out)
	}
	h.waitFor("session A", 3*time.Second, func() bool { return h.session("A") != nil })

	// 2. Permission prompt → waiting within 1 s.
	start := time.Now()
	h.run(hookJSON("A", "PermissionRequest", "/w/a", `,"tool_name":"Bash","tool_input":{"command":"rm -rf build"}`), "hook")
	h.waitFor("waiting", time.Second, func() bool { s := h.session("A"); return s != nil && s["status"] == "waiting" })
	t.Logf("permission → waiting in %v", time.Since(start))

	// 3. Two concurrent fake Claude sessions; CLI calls attributed by process tree.
	h.underClaude("wt-one", fmt.Sprintf(`echo '%s' | %s hook && %s step "one step"`, hookJSON("ONE", "SessionStart", "/w/shared", ""), h.bin, h.bin))
	h.underClaude("wt-two", fmt.Sprintf(`echo '%s' | %s hook && %s step "two step"`, hookJSON("TWO", "SessionStart", "/w/shared", ""), h.bin, h.bin))
	h.waitFor("ptree attribution", 2*time.Second, func() bool {
		one, two := h.session("ONE"), h.session("TWO")
		return one != nil && two != nil && one["step_text"] == "one step" && two["step_text"] == "two step"
	})

	// 4. Kill the daemon: calls stay fast and silent, next one respawns.
	h.killDaemon()
	t.Setenv("unused", "")
	code, out, took := h.run("", "step", "after kill")
	if code != 0 || out != "" || took > 2*time.Second {
		t.Fatalf("after kill: code=%d out=%q took=%v", code, out, took)
	}
	h.waitFor("respawn", 3*time.Second, func() bool { return h.view() != nil })

	// 5. Status line with null fields.
	code, out, took = h.run(`{"session_id":"A","context_window":{"used_percentage":null}}`, "statusline")
	if code != 0 || strings.TrimSpace(out) != "trk · ctx — · 5h — · 7d —" || took > time.Second {
		t.Fatalf("statusline: code=%d out=%q took=%v", code, out, took)
	}

	// 6. TRK_URL pointing nowhere: no spawn, exit 0, quick.
	cmd := exec.Command(h.bin, "step", "x")
	cmd.Env = append(h.env, "TRK_URL=http://127.0.0.1:1")
	t0 := time.Now()
	if err := cmd.Run(); err != nil || time.Since(t0) > 500*time.Millisecond {
		t.Fatalf("TRK_URL down: err=%v took=%v", err, time.Since(t0))
	}
	_ = io.Discard
}
```

- [ ] **Step 2: Run** — `go test -tags e2e ./e2e -v -count=1` → PASS (expect it to fail first only if earlier tasks have gaps — fix those, not the test).
- [ ] **Step 3: Commit** — `git add e2e && git commit -m "test(e2e): acceptance flow for spawn, waiting, attribution, respawn"`

---

### Task 13: Release — GoReleaser, install.sh, CI, README

**Files:**
- Create: `.goreleaser.yaml`, `install.sh`, `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `README.md`

**Interfaces:** none (packaging).

- [ ] **Step 1: `.goreleaser.yaml`**
```yaml
version: 2
project_name: trk
before:
  hooks:
    - go mod tidy
    - go test ./...
builds:
  - id: trk
    main: ./cmd/trk
    binary: trk
    env: [CGO_ENABLED=0]
    goos: [linux, darwin, windows]
    goarch: [amd64, arm64]
    flags: [-trimpath]
    ldflags:
      - -s -w -X github.com/GujaLomsadze/trk/internal/version.Version={{ .Version }}
archives:
  - name_template: "trk_{{ .Os }}_{{ .Arch }}"   # version-less so releases/latest/download/<name> works
    formats: [tar.gz]
    format_overrides:
      - goos: windows
        formats: [zip]
checksum:
  name_template: checksums.txt
changelog:
  use: github
homebrew_casks:
  - name: trk
    repository:
      owner: GujaLomsadze
      name: homebrew-tap
      token: "{{ .Env.TAP_GITHUB_TOKEN }}"
    homepage: https://github.com/GujaLomsadze/trk
    description: Live tracker for coding agents
    skip_upload: auto
scoops:
  - name: trk
    repository:
      owner: GujaLomsadze
      name: scoop-bucket
      token: "{{ .Env.TAP_GITHUB_TOKEN }}"
    homepage: https://github.com/GujaLomsadze/trk
    description: Live tracker for coding agents
    skip_upload: auto
```
Run `go run github.com/goreleaser/goreleaser/v2@latest check`; if a key is deprecated or renamed in the current version, follow its message. Then `go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean --skip=publish` → 6 archives in `dist/`; check `ls -la dist/*/trk*` sizes (target 10–15 MB) and `file dist/trk_linux_arm64*/trk` says statically linked.

- [ ] **Step 2: `install.sh`**
```sh
#!/bin/sh
# trk installer: curl -fsSL https://raw.githubusercontent.com/GujaLomsadze/trk/main/install.sh | sh
set -eu
REPO="GujaLomsadze/trk"
DIR="${TRK_INSTALL_DIR:-$HOME/.local/bin}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "trk: unsupported OS '$os' (on Windows use Scoop or the .zip from GitHub Releases)" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "trk: unsupported architecture '$arch'" >&2; exit 1 ;;
esac

asset="trk_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/latest/download"
[ -n "${TRK_VERSION:-}" ] && base="https://github.com/$REPO/releases/download/$TRK_VERSION"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading $asset"
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"
want=$(grep " $asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)
if command -v sha256sum >/dev/null 2>&1; then
  got=$(sha256sum "$tmp/$asset" | cut -d' ' -f1)
else
  got=$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)
fi
if [ -z "$want" ] || [ "$want" != "$got" ]; then
  echo "trk: checksum mismatch for $asset" >&2
  exit 1
fi
tar -xzf "$tmp/$asset" -C "$tmp" trk
mkdir -p "$DIR"
install -m 0755 "$tmp/trk" "$DIR/trk"
echo "trk installed to $DIR/trk"
case ":$PATH:" in
  *":$DIR:"*) ;;
  *) echo "note: $DIR is not on your PATH; add it to your shell profile" ;;
esac
echo "next: trk init"
```
Verify: `sh -n install.sh` and, if available, `shellcheck install.sh`. Dry test against the snapshot: serve `dist/` with `python3 -m http.server` and temporarily point `base` at it via `TRK_VERSION`-style override — or at minimum run the checksum/extract part by hand on `dist/trk_linux_amd64.tar.gz`.

- [ ] **Step 3: CI workflows**

`.github/workflows/ci.yml`:
```yaml
name: ci
on:
  push:
  pull_request:
jobs:
  test:
    strategy:
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - run: go vet ./...
      - run: go test -race ./...
      - if: runner.os != 'Windows'
        run: go test -tags e2e -count=1 ./e2e/...
```

`.github/workflows/release.yml`:
```yaml
name: release
on:
  push:
    tags: ["v*"]
permissions:
  contents: write
jobs:
  goreleaser:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version: stable
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          TAP_GITHUB_TOKEN: ${{ secrets.TAP_GITHUB_TOKEN }}
```
(Check the latest major versions of the three actions at execution time.)

- [ ] **Step 4: `README.md`** — sections: what it is (one paragraph from spec §1–2), install (install.sh, brew, scoop, `go install github.com/GujaLomsadze/trk/cmd/trk@latest`), quick start (`trk init` → start Claude Code → `trk open`), agent verbs table, config env vars, context bands (40/50/60/70 with the two hints), WSL note (spec §8), what `trk init` changes + backups + how to undo (restore the `.trk-backup-*` file), license placeholder line only if Guja has picked one.

- [ ] **Step 5: Commit** — `git add .goreleaser.yaml install.sh .github README.md && git commit -m "build: goreleaser, installer, ci and readme"`

---

### Task 14: Whole-branch verification and hand-off

- [ ] **Step 1: Full suite** — `go vet ./... && go test -race ./... && go test -tags e2e -count=1 ./e2e/...` → all PASS. Paste the summary lines.
- [ ] **Step 2: Cross-compile** — `for t in linux/amd64 linux/arm64 darwin/arm64 darwin/amd64 windows/amd64 windows/arm64; do GOOS=${t%/*} GOARCH=${t#*/} CGO_ENABLED=0 go build -o /dev/null ./cmd/trk || exit 1; done` → no errors.
- [ ] **Step 3: Spec acceptance walk-through (§11)** — tick each against evidence from the e2e test and the Task 11 screenshots. Items needing real hardware (macOS ARM, Windows x64) are verified by CI on first push — list them as "pending CI".
- [ ] **Step 4: Real Claude Code check — ask Guja first.** Running `trk init` for real edits `~/.claude/settings.json` (currently has `ccstatusline` with `refreshInterval: 10`, and SessionStart/UserPromptSubmit hooks) and appends to `~/.claude/CLAUDE.md` (his global rules). Backups are written, but do not run it without his explicit go-ahead. With approval: `go install ./cmd/trk` (or copy to `~/.local/bin`), `trk init`, start a new Claude Code session, confirm the card appears, a permission prompt flips it to WAITING ON YOU, and the ctx gauge tracks.
- [ ] **Step 5: Final review** — dispatch one reviewer (superpowers:requesting-code-review) over `main..m1`.
- [ ] **Step 6: Hand-off** — do NOT push. Give Guja: branch name, commit list, and the exact commands he'd run (`git push -u origin m1`, `gh pr create ...`, tag `v0.1.0`), plus repos/secrets he must create for brew/scoop (`GujaLomsadze/homebrew-tap`, `GujaLomsadze/scoop-bucket`, `TAP_GITHUB_TOKEN`) and the open license choice.

---

## Self-review notes

- Spec coverage: §4 verbs/serve/open/init/hook/statusline/version → Tasks 1, 8, 9, 10. §4 auto-spawn/never-fail/fast → Tasks 8, 9, 12. §5.1–5.3 → Task 10 (hook events re-verified, incl. `PermissionRequest`, `PostToolUseFailure`). §5.4 attribution → Tasks 5, 7, 12. §5.5 agent-agnostic → `api` source + synthetic cwd sessions (Task 5). §6.1–6.4 → Tasks 2, 4, 5, 6. §7 → Task 11 (plus the user's context bands). §8 install → Task 13 (npx/uvx wrappers and winget are M2: not in the M1 checklist). §10 M1 table-driven tests → Tasks 4, 5, 10. §11 → Tasks 12, 14.
- Deviations from the spec, on purpose: context warning uses the user's 40/50/60/70 bands instead of "warn ≥ 80%"; hooks are registered `async` (zero latency for agents); `prompt` clears `blocked`; status-line duplicates dropped before storage.
