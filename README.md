# TRK

A local, single-binary tracker that shows, live, what every coding agent on your machine is doing: progress, status, context usage, plan limits, and which agents need a human. Agents report through a tiny CLI; Claude Code hooks and the status line report automatically. A browser dashboard (phosphor CRT theme) shows the whole fleet.

TRK shows two things side by side: **declared intent** (`trk step "writing tests"`) and **ground truth** (every tool call, permission prompt and context % that Claude Code reports through hooks). The gap between them is the useful part, e.g. *"says step 3/5 writing tests; reality: pytest failed 14× in 6 min"*.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/GujaLomsadze/trk/main/install.sh | sh
```

Or `go install github.com/GujaLomsadze/trk/cmd/trk@latest`. Homebrew and Scoop packages are published with each release.

Binaries for Linux, macOS and Windows on amd64 and arm64 are on the [releases page](https://github.com/GujaLomsadze/trk/releases).

## Quick start

```sh
trk init     # add hooks + status line to ~/.claude/settings.json, add a CLAUDE.md block
             # (start or restart Claude Code)
trk open     # open http://localhost:7777
```

You never need to run `trk serve` yourself: the first `trk` call that finds no daemon starts one in the background.

## Agent verbs

| Command | Meaning |
|---|---|
| `trk start "<task>" --steps N` | Declare a task |
| `trk step "<what>"` | Declare the current step |
| `trk progress i/N` | Declare progress |
| `trk blocked "<question>"` | Ask the human for a decision (shows in **Needs you**) |
| `trk done "<result>"` | Declare completion |

Every reporting call uses a ~200 ms timeout and **always exits 0**. If TRK is down, agents don't notice.

Anything that can POST JSON can report too (Codex, Aider, scripts): `POST /v1/events` with `{"source":"api","kind":"step","session_id":"my-agent","payload":{"text":"..."}}`.

## Context bands

Each card tracks the session's context window usage:

| Usage | Gauge | Hint on the card |
|---|---|---|
| < 40% | normal | — |
| ≥ 40% | yellow | — |
| ≥ 50% | yellow | Start thinking about handing off / compacting |
| ≥ 60% | red | Start thinking about handing off / compacting |
| ≥ 70% | red | …plus: Don't trust architectural reasoning without re-grounding it |

## Statuses

`WAITING ON YOU` (permission prompt pending) · `BLOCKED` (agent ran `trk blocked`) · `DONE` (turn or task finished) · `IDLE` (nothing for 2 min) · `LOOPING?` (same command failing ≥5× in 10 min, or one file edited ≥8× with no progress report) · `WORKING`.

## Configuration

All optional:

| Variable | Default | Purpose |
|---|---|---|
| `TRK_PORT` | `7777` | Daemon port (binds 127.0.0.1 only) |
| `TRK_URL` | — | Full daemon URL override, e.g. to reach a daemon on the other side of WSL. Disables auto-spawn. |
| `TRK_DATA_DIR` | `~/.local/share/trk` · `~/Library/Application Support/trk` · `%APPDATA%\trk` | SQLite event log + daemon log |
| `TRK_SESSION` | — | Force which session CLI calls are attributed to |

## What `trk init` changes

- `~/.claude/settings.json` (or `$CLAUDE_CONFIG_DIR/settings.json`): adds an async `trk hook` command for `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `Notification`, `Stop`, `SubagentStop` and `PreCompact`. Your existing hooks and key order are kept.
- Status line: set to `trk statusline` if you have none. If you already have one, `trk init` offers to chain it (`trk statusline --then '<your command>'`): TRK records the data and your status line prints unchanged.
- `~/.claude/CLAUDE.md`: appends a marked `<!-- trk:begin -->…<!-- trk:end -->` block telling agents to report progress.

Before any write, the original is saved as `<file>.trk-backup-YYYYMMDD-HHMMSS`. To undo, copy the backup back. Running `trk init` again is a no-op. Use `--dry-run` to preview, `--no-statusline` / `--no-claude-md` to skip parts.

## WSL

Run `trk` inside WSL next to Claude Code. WSL2 forwards localhost, so open `http://localhost:7777` in your Windows browser. For agents in both WSL and native Windows, run one daemon and point the other side at it with `TRK_URL` (mirrored networking mode makes this seamless).

## Development

```sh
go test ./...                       # unit tests
go test -tags e2e ./e2e/...         # black-box acceptance (Linux/macOS)
TRK_PORT=17777 TRK_DATA_DIR=$(mktemp -d) go run ./cmd/trk serve &
TRK_PORT=17777 sh scripts/seed-demo.sh   # demo fleet for the dashboard
```
