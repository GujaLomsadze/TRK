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
Report progress with the ` + "`trk`" + ` CLI. It never fails, prints nothing and always exits 0, so don't check its output.
- At the start of a task: ` + "`trk start \"<task>\" --steps <N>`" + `
- When moving to a new step: ` + "`trk step \"<what you're doing>\"`" + ` and ` + "`trk progress <i>/<N>`" + `
- When you need a human decision: ` + "`trk blocked \"<question>\"`" + `
- When finished: ` + "`trk done \"<one-line result>\"`" + `

Attach ` + "`trk`" + ` to a shell command you were going to run anyway, so it adds no extra step:
` + "`trk step \"Writing tests\"; go test ./...`" + `
Put ` + "`trk`" + ` first and join with ` + "`;`" + ` (never pipe it), so the real command's output and exit code are unchanged.
Run ` + "`trk`" + ` on its own only when there is no command to attach it to.
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
