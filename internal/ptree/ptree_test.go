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
	if z, _ := parseStat([]byte("55 (claude) Z 1 55 55 0 -1")); !z.Zombie {
		t.Fatal("zombie state not parsed")
	}
	if _, ok := parseStat([]byte("garbage")); ok {
		t.Fatal("garbage parsed")
	}
}

func TestTable(t *testing.T) {
	table := map[int]proc{
		100: {PPID: 1, Name: "trk"},
		200: {PPID: 100, Name: "claude"},
		300: {PPID: 200, Name: "zsh"},
		400: {PPID: 1, Name: "vim"},
		500: {PPID: 1, Name: "claude", Zombie: true},
		900: {PPID: 900, Name: "loop"},
	}
	tb := Table{func(pid int) (proc, bool) { p, ok := table[pid]; return p, ok }}
	if !tb.IsClaude(200) || tb.IsClaude(400) || tb.IsClaude(500) || tb.IsClaude(12345) || tb.IsClaude(1) {
		t.Fatal("IsClaude wrong")
	}
	if !tb.Descends(300, 100) || !tb.Descends(200, 200) || tb.Descends(400, 100) || tb.Descends(900, 1) {
		t.Fatal("Descends wrong")
	}
}
