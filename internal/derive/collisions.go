package derive

import (
	"sort"
)

type FileTouch struct {
	Session string
	Path    string
	TS      int64
	Edit    bool
}

type Collision struct {
	Path     string   `json:"path"`
	Sessions []string `json:"sessions"`
	LastTS   int64    `json:"last_ts"`
}

// Collisions: ≥2 distinct active sessions touched the same file within
// CollisionWindow and at least one of them edited it. Callers pass only active sessions.
func Collisions(touches []FileTouch, now int64) []Collision {
	type acc struct {
		sessions map[string]bool
		edited   bool
		last     int64
	}
	byPath := map[string]*acc{}
	for _, t := range touches {
		if t.Path == "" || t.TS < now-CollisionWindow {
			continue
		}
		p := NormPath(t.Path)
		a := byPath[p]
		if a == nil {
			a = &acc{sessions: map[string]bool{}}
			byPath[p] = a
		}
		a.sessions[t.Session] = true
		a.edited = a.edited || t.Edit
		if t.TS > a.last {
			a.last = t.TS
		}
	}
	var out []Collision
	for p, a := range byPath {
		if len(a.sessions) < 2 || !a.edited {
			continue
		}
		c := Collision{Path: p, LastTS: a.last}
		for s := range a.sessions {
			c.Sessions = append(c.Sessions, s)
		}
		sort.Strings(c.Sessions)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LastTS != out[j].LastTS {
			return out[i].LastTS > out[j].LastTS
		}
		return out[i].Path < out[j].Path
	})
	return out
}
