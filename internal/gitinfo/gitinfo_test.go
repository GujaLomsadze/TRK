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
