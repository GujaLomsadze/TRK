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
