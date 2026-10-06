// Package daemon serves the TRK HTTP API, SSE stream and dashboard.
package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/GujaLomsadze/trk/internal/fleet"
	"github.com/GujaLomsadze/trk/internal/model"
	"github.com/GujaLomsadze/trk/internal/store"
	"github.com/GujaLomsadze/trk/internal/term"
	"github.com/GujaLomsadze/trk/internal/version"
	"github.com/GujaLomsadze/trk/web"
)

const maxBody = 4 << 20

type Server struct {
	st      *store.Store
	fl      *fleet.Fleet
	hub     *Hub
	log     *log.Logger
	now     func() int64
	dirty   atomic.Bool
	terms   *term.Manager // dashboard-started agents
	termsOn atomic.Bool
	// OnShutdown stops the daemon; set by Run. Used by `trk update` to restart into the new binary.
	OnShutdown func()
	// OnStop pauses TRK (hooks stop respawning it) and then shuts down; set by Run.
	OnStop func()
}

func NewServer(st *store.Store, fl *fleet.Fleet, logger *log.Logger) *Server {
	s := &Server{st: st, fl: fl, hub: NewHub(), log: logger, now: func() int64 { return time.Now().UnixMilli() }, terms: term.NewManager()}
	s.terms.OnChange = func() { s.dirty.Store(true) }
	return s
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", s.postEvent)
	mux.HandleFunc("GET /v1/sessions", s.getSessions)
	mux.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	mux.HandleFunc("POST /v1/sessions/{id}/dismiss", s.dismiss)
	mux.HandleFunc("POST /v1/settings", s.settings)
	mux.HandleFunc("GET /v1/terms", s.listTerms)
	mux.HandleFunc("POST /v1/terms", s.startTerm)
	mux.HandleFunc("DELETE /v1/terms/{id}", s.stopTerm)
	mux.HandleFunc("GET /v1/terms/{id}/ws", s.attachTerm)
	mux.HandleFunc("GET /v1/account", s.getAccount)
	mux.HandleFunc("GET /v1/limits/history", s.getLimitHistory)
	mux.HandleFunc("GET /v1/stream", s.stream)
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("POST /v1/shutdown", s.shutdown)
	mux.HandleFunc("POST /v1/stop", s.stop)
	mux.Handle("GET /", http.FileServerFS(web.FS))
	return guard(mux)
}

// guard blocks DNS rebinding (foreign Host) and cross-site requests (foreign Origin).
// The CLI sends no Origin; the dashboard is same-origin.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !localHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if o := r.Header.Get("Origin"); o != "" {
			u, err := url.Parse(o)
			if err != nil || !localHost(u.Host) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func localHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	switch strings.Trim(host, "[]") {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

func (s *Server) postEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	var env model.Envelope
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	now := s.now()
	ev, ok := s.fl.Normalize(env, now)
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err := s.st.Append(&ev); err != nil {
		s.log.Printf("append: %v", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	sess := s.fl.Apply(ev, now)
	if err := s.st.UpsertSession(sess); err != nil {
		s.log.Printf("upsert session: %v", err)
	}
	if ev.Kind == model.KindStatus {
		if err := s.st.SaveAccount(s.fl.Account()); err != nil {
			s.log.Printf("save account: %v", err)
		}
	}
	s.dirty.Store(true)
	w.WriteHeader(http.StatusAccepted)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) getSessions(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.dash())
}

func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sv, ok := s.fl.SessionView(id, s.now())
	if !ok {
		http.NotFound(w, r)
		return
	}
	evs, err := s.st.SessionEvents(id, 200)
	if err != nil {
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	if evs == nil {
		evs = []model.Event{}
	}
	writeJSON(w, map[string]any{"session": sv, "events": evs})
}

func (s *Server) dismiss(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	switch err := s.fl.Dismiss(id, s.now()); err {
	case nil:
	case fleet.ErrNotFound:
		http.NotFound(w, r)
		return
	default:
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if row, ok := s.fl.SessionRow(id); ok {
		if err := s.st.UpsertSession(row); err != nil {
			s.log.Printf("persist dismiss: %v", err)
		}
	}
	s.dirty.Store(true)
	w.WriteHeader(http.StatusNoContent)
}

// maxHideAfterMin caps the auto-hide setting at 30 days.
const maxHideAfterMin = 30 * 24 * 60

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		HideAfterMin     *int  `json:"hide_after_min"`
		TerminalsEnabled *bool `json:"terminals_enabled"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	badHide := body.HideAfterMin != nil && (*body.HideAfterMin < 0 || *body.HideAfterMin > maxHideAfterMin)
	if err != nil || badHide || (body.HideAfterMin == nil && body.TerminalsEnabled == nil) {
		http.Error(w, "want {\"hide_after_min\": 0..43200} (0 = never) and/or {\"terminals_enabled\": bool}", http.StatusBadRequest)
		return
	}
	if body.HideAfterMin != nil {
		m := *body.HideAfterMin
		s.fl.SetHideAfter(int64(m) * 60000)
		if err := s.st.SetSetting("hide_after_min", strconv.Itoa(m)); err != nil {
			s.log.Printf("save setting: %v", err)
		}
	}
	if on := body.TerminalsEnabled; on != nil {
		s.SetTerminals(*on)
		if err := s.st.SetSetting(settingTerminals, strconv.FormatBool(*on)); err != nil {
			s.log.Printf("save setting: %v", err)
		}
	}
	s.dirty.Store(true)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.fl.Account()) }

// limitWindows: plan window → stored payload key, window length, chart bucket (all ms).
var limitWindows = map[string]struct {
	key            string
	length, bucket int64
}{
	"5h": {"five_hour", 5 * 3600_000, 60_000},
	"7d": {"seven_day", 7 * 86400_000, 30 * 60_000},
}

// getLimitHistory serves the readings of the current 5h or 7d window for the Stats page.
func (s *Server) getLimitHistory(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("window")
	win, ok := limitWindows[name]
	if !ok {
		http.Error(w, "window must be 5h or 7d", http.StatusBadRequest)
		return
	}
	now := s.now()
	a := s.fl.Account()
	reset := a.FiveHReset
	if name == "7d" {
		reset = a.SevenDReset
	}
	if reset <= now { // unknown or stale: show the window that would end now
		reset = 0
	}
	start := now - win.length
	if reset > 0 {
		start = reset - win.length
	}
	pts, err := s.st.LimitHistory(win.key, start, win.bucket)
	if err != nil {
		s.log.Printf("limit history: %v", err)
		http.Error(w, "store error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"window": name, "start": start, "resets_at": reset, "now": now, "points": pts})
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true, "app": "trk", "version": version.Version})
}

func (s *Server) shutdown(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
	if s.OnShutdown != nil {
		go s.OnShutdown()
	}
}

func (s *Server) stop(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusAccepted)
	if s.OnStop != nil {
		go s.OnStop()
	}
}

func (s *Server) snapshot() []byte {
	b, err := json.Marshal(s.dash())
	if err != nil {
		s.log.Printf("snapshot: %v", err)
		return []byte("{}")
	}
	return b
}

func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	ch, cancel := s.hub.Subscribe()
	defer cancel()
	send := func(b []byte) error {
		if _, err := fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", b); err != nil {
			return err
		}
		fl.Flush()
		return nil
	}
	if send(s.snapshot()) != nil {
		return
	}
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case b := <-ch:
			if send(b) != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}

// Loop coalesces updates into ≤4 snapshots/s and refreshes every 5 s so
// time-based states (idle, ages, countdowns) advance without new events.
func (s *Server) Loop(ctx context.Context) {
	flush := time.NewTicker(250 * time.Millisecond)
	tick := time.NewTicker(5 * time.Second)
	defer flush.Stop()
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.dirty.Store(true)
		case <-flush.C:
			if s.dirty.Swap(false) {
				s.hub.Publish(s.snapshot())
			}
		}
	}
}
