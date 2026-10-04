package derive

import (
	"encoding/json"
	"path"
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
			file = NormPath(p)
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

// NormPath gives one spelling per file on every OS: forward slashes, cleaned.
// filepath.Clean alone turns /a/b into \a\b on Windows, which broke matching.
func NormPath(p string) string {
	if p == "" {
		return ""
	}
	return path.Clean(strings.ReplaceAll(p, `\`, "/"))
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

func rel(p, cwd string) string {
	if cwd != "" {
		if r, err := filepath.Rel(filepath.FromSlash(NormPath(cwd)), filepath.FromSlash(p)); err == nil && !strings.HasPrefix(r, "..") {
			return filepath.ToSlash(r)
		}
	}
	return p
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
