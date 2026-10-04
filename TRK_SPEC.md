# TRK.exe — Build Spec

A local, single-binary tracker that shows, live, what every coding agent on this machine is doing: progress, status, context usage, plan limits, and who needs a human. Agents report through a tiny CLI; Claude Code hooks and the status line report automatically. A browser dashboard (phosphor CRT theme) visualizes the fleet.

> Hand this file to Claude Code as the project brief. Verify Claude Code hook event names and status line JSON fields against the current docs (https://code.claude.com/docs) before wiring them; treat every incoming field as optional.

---

## 1. Goals and non-goals

**Goals**
- One static binary. Download, run, done. No runtime, no config, no external services.
- Runs on Linux, macOS, Windows, WSL, dev containers; x64 and ARM.
- Shows, for every agent session: declared task and progress, actual tool activity, status, context usage, cost, elapsed time.
- Shows account-wide Claude plan limits (5-hour and weekly) when available.
- Puts "needs you" (permission prompts, blocked questions) front and center.
- Never breaks or slows an agent. If TRK is down, agents don't notice.

**Non-goals (for now)**
- Not a remote control for Claude Code. It observes; it does not drive sessions.
- No cloud, no accounts, no multi-user. Localhost only.
- No ticketing / sprint planning.

---

## 2. Core idea: intent vs. reality

Two input channels, shown side by side:

1. **Declared intent** — the agent calls `trk start/step/progress/blocked/done`. Says what it *thinks* it's doing.
2. **Ground truth** — Claude Code hooks and status line POST events automatically: every tool call, file touched, command run, permission prompt, context %, cost. No agent cooperation needed.

The gap between them is the product's most valuable signal (e.g. "says step 3/5 writing tests; reality: pytest failed 14× in 6 min with the same assertion").

---

## 3. Tech stack

- **Language:** Go (latest stable). Standard library first.
- **Storage:** SQLite via `modernc.org/sqlite` (pure Go, **no CGO** — required for trivial cross-compilation).
- **Live updates:** Server-Sent Events from daemon to browser.
- **UI:** Static HTML/CSS/vanilla JS (no build step, no framework needed for MVP), embedded in the binary with `go:embed`. Fonts embedded too (no network at runtime).
- **Release:** GoReleaser → GitHub Releases for linux/darwin/windows × amd64/arm64, checksums, Homebrew tap, Scoop manifest.
- Target binary size ~10–15 MB.

---

## 4. One binary, several roles

```
trk serve                     # run daemon + dashboard (usually auto-spawned)
trk open                      # open dashboard in browser
trk init                      # one-time setup (hooks, status line, CLAUDE.md snippet)

# agent-facing reporting verbs (keep this set tiny)
trk start "Refactor Kafka consumer" --steps 5
trk step "Writing tests"
trk progress 3/5
trk blocked "Drop or keep legacy topic during cutover?"
trk done "Tests green, PR ready"

# integration roles (called by Claude Code, not humans)
trk hook                      # reads hook JSON on stdin, forwards to daemon
trk statusline                # reads status line JSON on stdin, forwards, prints a compact line

trk version
```

### Non-negotiable CLI behavior
- **Auto-spawn:** any command that finds no daemon starts `trk serve` in the background (detached), then continues. Users never need to remember to start it.
- **Never fail the caller:** every reporting/hook/statusline call has a ~200 ms timeout and **always exits 0**. Daemon down → silent no-op. No stderr noise.
- **Fast:** the CLI does one HTTP POST and exits.

### Config (all optional)
- `TRK_PORT` (default `7777`), `TRK_URL` (full override, e.g. WSL ↔ Windows), `TRK_DATA_DIR`.
- Data dir defaults: Linux `~/.local/share/trk/`, macOS `~/Library/Application Support/trk/`, Windows `%APPDATA%\trk\`.
- Bind to `127.0.0.1` only.

---

## 5. Integrations

### 5.1 Claude Code hooks → `trk hook`
`trk init` merges hook entries into `~/.claude/settings.json` pointing at `trk hook` for the relevant events (verify names in docs; expected: `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Notification`, `Stop`, `SubagentStop`, `PreCompact`). The handler forwards the raw JSON payload plus a timestamp; parsing happens in the daemon.

Hook payloads include `session_id`, `cwd`, `transcript_path` → primary session key.

### 5.2 Status line → `trk statusline`
Claude Code pipes session JSON to the configured status line command on every update. Fields to use (all optional):
- `session_id`, `cwd`, `model.display_name`, `workspace`, `version`, `cost`
- `context_window.used_percentage`, `remaining_percentage`, `context_window_size`, `total_input_tokens`, `total_output_tokens`
- `rate_limits.five_hour.{used_percentage,resets_at}`, `rate_limits.seven_day.{used_percentage,resets_at}` (Pro/Max only; known to go missing in some versions — show "—")

`trk statusline` forwards the JSON, then prints a compact line (e.g. `trk · ctx 42% · 5h 23% · 7d 61%`).
**If the user already has a status line**, `trk init` must not overwrite it: offer to chain (`trk statusline --then "<original command>"`, where TRK forwards the data then runs and prints the original script's output).

### 5.3 Agent instructions (CLAUDE.md snippet)
`trk init` prints/appends this (idempotent, marked block):

```md
<!-- trk:begin -->
## Progress reporting
Report progress with the `trk` CLI (it never fails; don't check its output):
- At the start of a task: `trk start "<task>" --steps <N>`
- When moving to a new step: `trk step "<what you're doing>"` and `trk progress <i>/<N>`
- When you need a human decision: `trk blocked "<question>"`
- When finished: `trk done "<one-line result>"`
<!-- trk:end -->
```

### 5.4 Session attribution (which agent sent a CLI call?)
Hooks and status line carry `session_id`; plain `trk step` calls don't. Resolve in this order:
1. `TRK_SESSION` env var if set.
2. **Process-tree walk:** CLI walks parent PIDs to find the ancestor `claude` process; hooks/status line do the same. Daemon maps `claude PID → session_id`. (Linux/WSL: `/proc/<pid>/stat`; macOS: `sysctl`/`ps`; Windows: Toolhelp snapshot.)
3. Fallback: `cwd` + git branch → most recent active session there.
Store the resolution method on each event for debugging.

### 5.5 Agent-agnostic
Everything goes through a plain HTTP/JSON API, so Codex, Aider, or scripts can report too. Claude Code is just the first-class integration.

---

## 6. Daemon

### 6.1 Data model (append-only event log; state is derived)
```sql
events(
  id INTEGER PRIMARY KEY,
  ts INTEGER NOT NULL,            -- unix ms
  session_id TEXT,
  source TEXT NOT NULL,           -- 'cli' | 'hook' | 'statusline' | 'api'
  kind TEXT NOT NULL,             -- start|step|progress|blocked|done|tool_pre|tool_post|notification|stop|compact|status|...
  payload TEXT NOT NULL,          -- raw JSON
  attribution TEXT                -- 'env'|'ptree'|'cwd'|'native'
);
sessions(                         -- materialized from events, rebuildable
  session_id TEXT PRIMARY KEY,
  name TEXT, cwd TEXT, repo TEXT, branch TEXT, model TEXT,
  task TEXT, step_text TEXT, step_i INTEGER, step_n INTEGER,
  status TEXT,                    -- working|waiting|blocked|idle|looping|done
  ctx_pct REAL, ctx_size INTEGER, cost_usd REAL, tokens_in INTEGER, tokens_out INTEGER,
  started_at INTEGER, last_event_at INTEGER
);
account(                          -- latest plan-limit snapshot
  five_h_pct REAL, five_h_reset INTEGER, seven_d_pct REAL, seven_d_reset INTEGER, updated_at INTEGER
);
```
Index `events(session_id, ts)`. WAL mode.

### 6.2 HTTP API (localhost)
```
POST /v1/events            # CLI, hook, statusline, third-party
GET  /v1/sessions          # current fleet state
GET  /v1/sessions/:id      # detail + recent events
GET  /v1/account           # plan limits snapshot
GET  /v1/stream            # SSE: session + account updates
GET  /                     # embedded dashboard
GET  /healthz
```

### 6.3 Derived status rules (MVP)
- `waiting` — last Notification is a permission prompt not yet followed by tool activity.
- `blocked` — last declared event is `blocked`.
- `done` — `done` declared or `Stop`/`SessionEnd` with no later activity.
- `idle` — no events for > 2 min while not done.
- `looping` (heuristic) — same command/tool+args failing ≥ 5 times within 10 min, or same file edited back and forth repeatedly with no progress change.
- Otherwise `working`.

### 6.4 Collision detection
Two active sessions touching the same file path within 10 min → collision entry.

---

## 7. Dashboard UI

### 7.1 MVP layout (Fleet view, single page, responsive)
1. **Header bar:** title `TRK.EXE`, subtitle (`fleet monitor · localhost:7777`), stats (agents, working, needs you, tokens today), **plan limits** (5h and 7d bars with reset countdowns), view nav (Fleet now; Timeline/Orbit later).
2. **Main grid (left, ~2/3):** one card per agent session. Sort: needs-you first, then working, then idle/done.
3. **Right column (~1/3):** **Needs you** inbox (permission prompts and `blocked` questions from all agents) and **Collisions**.
4. **Bottom:** **Last 30 minutes** swimlane timeline, one lane per agent, blocks colored by activity (read / edit / run / waiting on you), horizontal scroll on narrow screens.

**Agent card contents:**
- name (derived: worktree/dir name or task slug), `repo · branch`
- status chip (WORKING / WAITING ON YOU / BLOCKED / LOOPING? / IDLE / DONE)
- declared task
- `step i/N · step text` + progress bar
- **reality box** (only when intent and reality diverge, e.g. loop detected)
- last 3 tool calls (`› Edit path`, `› Bash cmd ✓/✗`)
- **context gauge** (`ctx 42% of 200K`; warn ≥ 80%)
- footer: elapsed, tokens, cost

Cards with waiting/blocked/looping status get the attention border.

**Needs-you inbox item:** agent · type · age, the question/command. MVP is read-only (shows what's pending; the human answers in the terminal). Answering from the dashboard is a later feature, not MVP.

### 7.2 Phosphor theme (the default and only theme for MVP)

Principle: **CRT style on things you scan, clean type on things you read.** Glow and bitmap font only on title, chips, buttons, ASCII bars. Tasks, steps, tool lines, inbox text use clean mono with no glow.

**Colors**
| Token | Value | Use |
|---|---|---|
| bg | `#0B0605` | page |
| panel | `#110908` | cards, header, inbox |
| border | `#3A1A16` | card/panel borders |
| rule-soft | `#2A1411` | inner dividers, inbox items |
| text | `#F1DCD6` | primary readable text (warm off-white) |
| muted | `#B9928A` | secondary text, meta |
| accent | `#FF5A4A` | phosphor red: title, active, progress |
| accent-fg | `#0B0605` | text on accent fills |
| warn | `#FFB27A` | amber: needs-you, reality box, waiting bars |
| hot | `#6E2A20` | attention card border |
| done | `#7A5650` | done progress |
| track | `#2A1411` | progress track |
| chip accent | bg `rgba(255,90,74,0.10)`, fg `#FF7A6B` | WORKING chip |
| chip warn | bg `rgba(255,178,122,0.10)`, fg `#FFB27A` | WAITING / BLOCKED / LOOPING? |
| chip done | transparent, fg `#B9928A` | DONE / IDLE |
| timeline | read `#3A1A16`, edit `#FF5A4A`, run `#F1DCD6`, wait `#FFB27A` | swimlane blocks |

**Typography**
- Readable text: **IBM Plex Mono** — body 14px, small/meta 12px, card names & section headings 16px semibold, section headings uppercase.
- Decorative: **VT323** — title 36px with `letter-spacing: 0.08em`; chips/buttons/ASCII bars 19px, uppercase.
- Glow: title `text-shadow: 0 0 10px rgba(255,90,74,0.7)`; chips/ASCII bars/secondary buttons `0 0 6px rgba(255,90,74,0.55)`. **No glow on body text.**

**Shape & effects**
- Radius 2px on panels/buttons, 0 on chips.
- Cards: 1px border + `box-shadow: inset 0 1px 0 rgba(255,90,74,0.18)`.
- Progress = ASCII bar in VT323, 20 cells: `[████████████░░░░░░░░]`, colored by status (accent / warn / done).
- Scanlines overlay on the whole page, pointer-events none: `repeating-linear-gradient(to bottom, transparent 0 2px, rgba(0,0,0,0.16) 3px, transparent 4px)`.
- Primary button (e.g. Allow, later): accent fill, accent-fg text, no glow. Secondary: transparent, border, glow.
- Touch targets ≥ 44px. Contrast: body text ≥ 4.5:1 against panel (the palette above meets this).
- Implement as CSS custom properties on `:root` so more themes can be added later as token sets.

### 7.3 Later themes (already prototyped, not MVP)
Grid (Tron, cyan on blue-black, faint background grid, Geist/Geist Mono), Ares (graphite + red light edges), Cyberpunk, Modern (light, Linear-like), Newspaper ("The Fleet Gazette", Playfair + Source Serif). Same layout, different tokens + small flourishes.

---

## 8. Install & distribution

- `curl -fsSL https://<host>/install.sh | sh` → detects OS/arch, installs to `~/.local/bin` (or `/usr/local/bin`), prints next step: `trk init`.
- Homebrew tap, Scoop manifest, winget later.
- `npx trk` / `uvx trk` thin wrappers that download the matching release binary (anyone running agents has Node or uv).
- `go install github.com/<you>/trk@latest`.

**WSL note:** run `trk` inside WSL next to Claude Code; WSL2 forwards localhost, so open `http://localhost:7777` in the Windows browser. For agents in both WSL and native Windows, run one daemon and point the other side at it with `TRK_URL` (mirrored networking mode makes this seamless).

---

## 9. Suggested repo layout

```
trk/
  cmd/trk/main.go             # subcommand dispatch
  internal/cli/               # start/step/progress/blocked/done, hook, statusline, init, open
  internal/client/            # tiny HTTP client w/ timeout, always-exit-0 wrapper, auto-spawn
  internal/daemon/            # HTTP server, SSE hub, API handlers
  internal/store/             # SQLite schema, migrations, event append, materializer
  internal/derive/            # status rules, loop detection, collisions
  internal/ptree/             # parent-process walk per OS (build tags)
  internal/claudecfg/         # safe merge of ~/.claude/settings.json, CLAUDE.md block
  web/                        # index.html, app.js, theme.css, fonts/ (go:embed)
  .goreleaser.yaml
  install.sh
```

---

## 10. Milestones

**M1 — MVP (ship this first)**
- [ ] `serve`, auto-spawn, `open`, `version`
- [ ] Reporting verbs with 200 ms timeout / exit-0 guarantee
- [ ] `hook` and `statusline` (with chaining)
- [ ] `init`: safe, idempotent settings.json merge + CLAUDE.md block, with backup of the original file
- [ ] SQLite event log + materialized sessions + account snapshot
- [ ] Session attribution: env → process tree → cwd/branch
- [ ] Derived statuses incl. basic loop detection; collisions
- [ ] SSE + Fleet view in phosphor theme (cards, needs-you inbox, collisions, plan limits, 30-min timeline)
- [ ] GoReleaser for 6 targets + install.sh
- [ ] Table-driven tests for: status derivation, loop heuristic, settings.json merge, attribution fallback

**M2**
- Session drill-down (full event log, files touched, commands, plan checklist)
- Desktop notifications (blocked / waiting / done)
- Full Timeline view with replay scrubber
- History & stats (per task duration, cost, stuck rate)

**M3 — the distinctive stuff**
- **Heartbeat lines:** ECG-style trace per agent (spike per tool call; loop = identical repeating spikes; stuck = flatline)
- **Fog-of-war code map:** repo treemap; read files light up, edited files glow, untouched stay dark; highlights blind spots (edited X without reading its importer Y)
- **Orbit view:** agents orbit "you"; ring = progress; dots = files; amber tether = needs you; shared file = collision node
- Drift meter (declared vs. actual files/steps/time)
- Voice "status" briefing, ambient sound cues, menubar / LED ambient state
- After-action report per finished session
- Subagent hierarchy with rolled-up progress
- Theme switcher (Grid, Ares, Cyberpunk, Modern, Newspaper)

---

## 11. Acceptance criteria for M1

- Fresh machine: install one binary, run `trk init`, start Claude Code → the session appears on `localhost:7777` within seconds, with no manual `trk serve`.
- Kill the daemon mid-session → the agent continues with zero errors; next `trk` call respawns it.
- Two concurrent Claude Code sessions in different worktrees are shown as separate cards with correct attribution of CLI calls.
- Permission prompt in a session → card turns WAITING ON YOU and the item appears in the inbox within 1 s.
- Context % and (when present) 5h/7d limits update live; missing fields render as "—".
- Same binary build works on Linux x64, Linux ARM, macOS ARM, Windows x64, and inside WSL2.
