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
trk init --dry-run   # preview exactly what would change (writes nothing)
trk init     # add hooks + status line to ~/.claude/settings.json, add a CLAUDE.md block
             # (start or restart Claude Code)
trk open     # open http://localhost:7777
```

Worried about your Claude setup? Read [How TRK works with Claude Code](#how-trk-works-with-claude-code-and-why-it-wont-break-it): it's observe-only, async, backed up and reversible.

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

## How TRK works with Claude Code (and why it won't break it)

TRK **watches** Claude Code. It doesn't drive it, and it can't change what Claude does.

```
 Claude Code ──hook event──▶ trk hook ─┐
     │                                  │  one local HTTP POST,
     ├─status line JSON──▶ trk statusline ─┤  200 ms timeout,
     │                                  │  always exits 0
     └─runs──▶ trk step "…" (agent) ────┘
                                        ▼
                         trk daemon (127.0.0.1:7777) ──▶ SQLite in your data dir
                                        │
                                        └──SSE──▶ dashboard in your browser
```

### What `trk init` changes

Exactly three things, all reversible:

1. **Hooks** in `~/.claude/settings.json`: one `trk hook` entry for each of `SessionStart`, `SessionEnd`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `Notification`, `Stop`, `SubagentStop`, `PreCompact`. They're added alongside your existing hooks. Nothing of yours is removed or reordered.
2. **Status line.** If you have none, it becomes `trk statusline`. If you already have one (e.g. `ccstatusline`), TRK asks before chaining it: `trk statusline --then '<your command>'`. Your command gets the same input, and its output is printed **unchanged**. Other settings like `padding` and `refreshInterval` are kept.
3. **CLAUDE.md:** a short block between `<!-- trk:begin -->` and `<!-- trk:end -->` that asks agents to report progress with `trk start/step/progress/blocked/done`.

Before writing, TRK saves each original as `<file>.trk-backup-YYYYMMDD-HHMMSS`. Running `trk init` again changes nothing. To see the exact changes first: `trk init --dry-run`. To skip parts: `--no-statusline`, `--no-claude-md`.

### Why it can't slow down, block or break Claude

| Worry | What actually happens |
|---|---|
| "Hooks will slow every tool call" | Hooks are registered with `"async": true`, so Claude starts them and moves on without waiting. |
| "A hook could approve or deny things" | Async hook output is discarded by Claude Code, and `trk hook` prints nothing anyway. TRK cannot approve, deny, block or inject anything into Claude's context. |
| "If TRK crashes, Claude breaks" | Every `trk` call gives up after ~200 ms and **always exits 0**. Daemon down → silent no-op. The next call restarts it. |
| "It messes up my status line" | A chained status line prints your command's output byte for byte. TRK only reads the JSON on the way through. |
| "It phones home" | No. The daemon binds `127.0.0.1` only and makes no outbound requests. Requests from web pages (foreign `Origin`/`Host`) are refused. |
| "It rewrites my settings badly" | Invalid JSON → TRK stops and writes nothing. Key order, number formatting, file permissions and symlinked dotfiles are preserved. |

### Good to know

- **What gets stored.** Hook payloads go into a local SQLite file in your data dir (`~/.local/share/trk/` on Linux). They can include your prompts, shell commands, file paths and snippets of file contents (large values are trimmed). Nothing leaves your machine. Delete the folder any time to wipe it.
- **Agents calling `trk`.** Because of the CLAUDE.md block, agents run `trk …` through their Bash tool, which may trigger a permission prompt the first time. To allow it once and for all, add this to `~/.claude/settings.json`:
  ```json
  "permissions": { "allow": ["Bash(trk:*)"] }
  ```
  Each call costs the agent a few tokens.
- **Uninstall in the right order.** If you delete the `trk` binary but leave the hooks in place, Claude Code will report hook errors and a chained status line will go blank. Undo first (below), then remove the binary.

### Undo

```sh
# 1. put the originals back (pick the newest backup of each)
cp ~/.claude/settings.json.trk-backup-<timestamp> ~/.claude/settings.json
cp ~/.claude/CLAUDE.md.trk-backup-<timestamp>     ~/.claude/CLAUDE.md
# 2. stop the daemon and remove trk
kill "$(cat ~/.local/share/trk/trk.pid)"
rm -r ~/.local/bin/trk ~/.local/share/trk
```

Edited the files since? Undo by hand instead: delete the hook entries whose command ends in `trk hook`, change `statusLine.command` back to the part after `--then` (without the quotes), and delete the `trk:begin`…`trk:end` block from CLAUDE.md.

## WSL

Run `trk` inside WSL next to Claude Code. WSL2 forwards localhost, so open `http://localhost:7777` in your Windows browser. For agents in both WSL and native Windows, run one daemon and point the other side at it with `TRK_URL` (mirrored networking mode makes this seamless).

## Development

Requires Go (see `go.mod`). Everything runs through `make`; `make help` lists all targets. Dev targets use port **17777** and `/tmp/trk-dev`, so they never touch a real TRK on :7777 or your real `~/.claude`.

```sh
make demo          # build, start a dev daemon in the background, seed a fake fleet
                   # → http://localhost:17777
make stop          # stop it
make run           # dev daemon in the foreground instead (Ctrl+C to stop)
make seed          # post the demo events again
make logs          # tail the dev daemon log

make test          # unit tests
make e2e           # end-to-end acceptance test (Linux/macOS)
make check         # vet + race tests + e2e + gofmt, what CI runs

make init-sandbox  # run `trk init` on a copy of your settings in /tmp, show the result
make init-dry      # what `trk init` would change in your real ~/.claude (writes nothing)
make install       # build and install to ~/.local/bin, then run `trk init` yourself

make cross         # build all 6 release targets into ./bin
make snapshot      # GoReleaser dry run into ./dist
make clean         # remove bin/, dist/ and dev data
```

The dashboard (`web/`) is embedded in the binary, so restart the daemon after changing it (`make stop demo`).
