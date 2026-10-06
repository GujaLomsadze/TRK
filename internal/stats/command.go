package stats

import (
	"path"
	"strings"
)

// setup commands that only prepare the shell; the command after them is the one that matters
var setup = map[string]bool{
	"cd": true, "pushd": true, "popd": true, "export": true, "unset": true, "set": true, "source": true, ".": true,
	"echo": true, "printf": true, "true": true, "sleep": true, "mkdir": true, "clear": true,
}

// trk's progress verbs, chained in front of real commands ("trk step x; go test")
var trkProgress = map[string]bool{"start": true, "step": true, "progress": true, "done": true, "blocked": true}

// wrappers run the next word as the command
var wrappers = map[string]bool{"sudo": true, "env": true, "time": true, "nohup": true, "exec": true, "command": true, "builtin": true}

// tools whose first non-flag argument names what they do ("go test", "git push", "make check")
var withSub = map[string]bool{
	"go": true, "git": true, "make": true, "npm": true, "npx": true, "pnpm": true, "yarn": true, "bun": true, "cargo": true,
	"docker": true, "kubectl": true, "gh": true, "terraform": true, "uv": true, "pip": true, "poetry": true, "dotnet": true,
	"mvn": true, "gradle": true, "helm": true, "aws": true, "gcloud": true, "systemctl": true, "brew": true, "apt": true,
}

// CommandKey names a Bash command by the program that does the work, so runs group together:
// "cd /repo && export X=1; make check 2>&1 | tail" → "make check". Setup steps (cd, export,
// trk progress calls, VAR=value) are skipped; pipes keep only their first stage.
func CommandKey(cmd string) string {
	first := ""
	for _, seg := range splitCommands(cmd) {
		w := strings.Fields(strings.TrimLeft(seg, "({ \t")) // subshells and groups: ( cmd ) / { cmd; }
		for len(w) > 0 && (isAssign(w[0]) || wrappers[w[0]]) {
			// X=$(cmd ...): the command substitution is what runs
			if _, v, _ := strings.Cut(w[0], "="); isAssign(w[0]) && strings.HasPrefix(v, "$(") && len(v) > 2 {
				w[0] = v[2:]
				break
			}
			w = w[1:]
		}
		if len(w) == 0 {
			continue
		}
		prog := path.Base(strings.Trim(w[0], `"'(`))
		if strings.Trim(prog, ")}") == "" {
			continue // the closing half of ( ... ) or { ...; }
		}
		if first == "" {
			first = prog
		}
		if setup[prog] || prog == "trk" && len(w) > 1 && trkProgress[w[1]] {
			continue
		}
		if withSub[prog] {
			for _, a := range w[1:] {
				if a != "" && !strings.HasPrefix(a, "-") && !strings.ContainsAny(a, "=/$\"'") {
					return prog + " " + a
				}
			}
		}
		return prog
	}
	if first == "" {
		return "(empty)"
	}
	return first
}

// splitCommands cuts a command line at ;, &&, ||, newlines and pipes (keeping each pipe's first stage).
func splitCommands(cmd string) []string {
	var out []string
	var b strings.Builder
	inPipe := false
	flush := func() {
		if s := strings.TrimSpace(b.String()); s != "" && !inPipe {
			out = append(out, s)
		}
		b.Reset()
	}
	q := rune(0)
	rs := []rune(cmd)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		if q != 0 {
			if c == q {
				q = 0
			}
			b.WriteRune(c)
			continue
		}
		switch {
		case c == '\'' || c == '"':
			q = c
			b.WriteRune(c)
		case c == ';' || c == '\n':
			flush()
			inPipe = false
		case c == '&' && i+1 < len(rs) && rs[i+1] == '&', c == '|' && i+1 < len(rs) && rs[i+1] == '|':
			flush()
			inPipe = false
			i++
		case c == '|':
			flush()
			inPipe = true // later stages of a pipe (| tail, | grep) aren't the command
		case c == '&' && !(i > 0 && rs[i-1] == '>') && !(i+1 < len(rs) && rs[i+1] == '>'): // background, not 2>&1 or &>
			flush()
		default:
			b.WriteRune(c)
		}
	}
	flush()
	return out
}

func isAssign(w string) bool {
	i := strings.IndexByte(w, '=')
	if i <= 0 {
		return false
	}
	for _, c := range w[:i] {
		if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
