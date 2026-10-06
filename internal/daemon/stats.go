package daemon

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/GujaLomsadze/trk/internal/stats"
	"github.com/GujaLomsadze/trk/internal/store"
)

const (
	statsMaxDays = 90
	statsTTL     = 30 * 1000 // ms a computed page is reused; the page is history, not live
	dayMs        = 24 * 3600 * 1000
)

type statsCache struct {
	mu   sync.Mutex
	key  string
	at   int64
	body stats.Result
}

// getStats serves the Stats page: GET /v1/stats?days=7&tz=-120&repos=TRK,arrow
// days counts local calendar days ending today (1–90); tz is the browser's getTimezoneOffset().
func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	days, err := strconv.Atoi(q.Get("days"))
	if q.Get("days") == "" {
		days, err = 7, nil
	}
	tz, tzErr := strconv.Atoi(q.Get("tz"))
	if q.Get("tz") == "" {
		tz, tzErr = 0, nil
	}
	if err != nil || days < 1 || days > statsMaxDays || tzErr != nil || tz < -14*60 || tz > 14*60 {
		http.Error(w, "want ?days=1..90&tz=<minutes, as getTimezoneOffset()>&repos=a,b", http.StatusBadRequest)
		return
	}
	var repos map[string]bool
	var keys []string
	for _, p := range strings.Split(q.Get("repos"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			if repos == nil {
				repos = map[string]bool{}
			}
			repos[p] = true
			keys = append(keys, p)
		}
	}
	sort.Strings(keys)
	now := s.now()
	key := strconv.Itoa(days) + "|" + strconv.Itoa(tz) + "|" + strings.Join(keys, ",")
	s.stats.mu.Lock()
	defer s.stats.mu.Unlock() // one computation at a time; a burst of reloads waits for the first
	if s.stats.key == key && now-s.stats.at < statsTTL {
		writeJSON(w, s.stats.body)
		return
	}
	off := int64(tz) * 60000
	since := floorDiv(now-off, dayMs)*dayMs + off - int64(days-1)*dayMs
	in := stats.Input{PrevSince: since - int64(days)*dayMs, Since: since, Until: now + 1, Days: days, TZOffsetMin: tz, Repos: repos,
		Prior: map[string]store.StatRow{}, Meta: map[string]stats.Meta{}}
	if in.Rows, err = s.st.StatRows(in.PrevSince, in.Until); err != nil {
		s.log.Printf("stats: %v", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	for _, row := range in.Rows {
		if _, done := in.Prior[row.Session]; done || row.Kind != "status" {
			continue
		}
		in.Prior[row.Session] = store.StatRow{} // looked up (zero = none before the range)
		if p, ok, err := s.st.LastStatus(row.Session, in.PrevSince); err == nil && ok {
			in.Prior[row.Session] = p
		}
	}
	rows, err := s.st.Sessions()
	if err != nil {
		s.log.Printf("stats sessions: %v", err)
	}
	for _, x := range rows {
		name := x.Title
		if name == "" {
			name = x.Name
		}
		in.Meta[x.SessionID] = stats.Meta{Name: name, Repo: x.Repo, Model: x.Model, Cwd: x.Cwd}
	}
	res := stats.Compute(in)
	s.stats.key, s.stats.at, s.stats.body = key, now, res
	writeJSON(w, res)
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}
