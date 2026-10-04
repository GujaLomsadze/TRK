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
