// Package gitinfo reads repo name and branch straight from .git files (no git exec, so it's cheap).
package gitinfo

import (
	"os"
	"path/filepath"
	"strings"
)

type Info struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
}

func Lookup(cwd string) Info {
	if cwd == "" {
		return Info{}
	}
	dir := filepath.Clean(cwd)
	for {
		dotgit := filepath.Join(dir, ".git")
		if st, err := os.Stat(dotgit); err == nil {
			gitDir, common := dotgit, dotgit
			if !st.IsDir() { // worktree or submodule: ".git" file points elsewhere
				b, err := os.ReadFile(dotgit)
				if err != nil {
					return Info{}
				}
				gd := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(b)), "gitdir:"))
				if !filepath.IsAbs(gd) {
					gd = filepath.Join(dir, gd)
				}
				gitDir, common = gd, gd
				if c, err := os.ReadFile(filepath.Join(gd, "commondir")); err == nil {
					cd := strings.TrimSpace(string(c))
					if !filepath.IsAbs(cd) {
						cd = filepath.Join(gd, cd)
					}
					common = filepath.Clean(cd)
				}
			}
			return Info{Repo: repoName(common, dir), Branch: branch(gitDir)}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Info{}
		}
		dir = parent
	}
}

func repoName(common, workdir string) string {
	if filepath.Base(common) == ".git" {
		return filepath.Base(filepath.Dir(common))
	}
	if strings.Contains(filepath.ToSlash(common), "/.git/") { // submodule: .git/modules/<name>
		return filepath.Base(workdir)
	}
	return strings.TrimSuffix(filepath.Base(common), ".git") // bare repo
}

func branch(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: "); ok {
		return strings.TrimPrefix(ref, "refs/heads/")
	}
	if len(head) >= 7 {
		return head[:7]
	}
	return head
}
