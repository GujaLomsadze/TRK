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
	"strings"
	"sync/atomic"
	"time"

	"github.com/GujaLomsadze/trk/internal/fleet"
	"github.com/GujaLomsadze/trk/internal/model"
	"github.com/GujaLomsadze/trk/internal/store"
	"github.com/GujaLomsadze/trk/internal/version"
	"github.com/GujaLomsadze/trk/web"
)

const maxBody = 4 << 20

type Server struct {
	st    *store.Store
	fl    *fleet.Fleet
	hub   *Hub
	log   *log.Logger
	now   func() int64
	dirty atomic.Bool
}

func NewServer(st *store.Store, fl *fleet.Fleet, logger *log.Logger) *Server {
	return &Server{st: st, fl: fl, hub: NewHub(), log: logger, now: func() int64 { return time.Now().UnixMilli() }}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", s.postEvent)
	mux.HandleFunc("GET /v1/sessions", s.getSessions)
	mux.HandleFunc("GET /v1/sessions/{id}", s.getSession)
	mux.HandleFunc("GET /v1/account", s.getAccount)
	mux.HandleFunc("GET /v1/stream", s.stream)
	mux.HandleFunc("GET /healthz", s.healthz)
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
	writeJSON(w, s.fl.View(s.now()))
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

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.fl.Account()) }

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"ok": true, "app": "trk", "version": version.Version})
}

func (s *Server) snapshot() []byte {
	b, err := json.Marshal(s.fl.View(s.now()))
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
