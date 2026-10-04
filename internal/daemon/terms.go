package daemon

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/GujaLomsadze/trk/internal/fleet"
	"github.com/GujaLomsadze/trk/internal/term"
	"github.com/gorilla/websocket"
)

// Experimental: agents started from the dashboard, each running in a pty owned by the
// daemon. Off unless the user turns it on in the Card layout drawer (stored server-side,
// so a page can't enable it for itself). The same Host/Origin guard as every route applies.

const settingTerminals = "experimental_terminals"

// dashView is the dashboard snapshot: the fleet plus the experimental terminal state.
type dashView struct {
	fleet.View
	ExperimentalTerminals bool        `json:"experimental_terminals"`
	TerminalsSupported    bool        `json:"terminals_supported"`
	Terminals             []term.Info `json:"terminals"`
}

func (s *Server) dash() dashView {
	return dashView{View: s.fl.View(s.now()), ExperimentalTerminals: s.termsOn.Load(),
		TerminalsSupported: term.Supported, Terminals: s.terms.List()}
}

// SetTerminals turns the experimental terminals on or off (Run restores it from the store).
func (s *Server) SetTerminals(on bool) { s.termsOn.Store(on); s.dirty.Store(true) }

// CloseTerminals kills every dashboard-started agent; Run calls it on shutdown.
func (s *Server) CloseTerminals() { s.terms.Close() }

func (s *Server) listTerms(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"enabled": s.termsOn.Load(), "running": s.terms.Running(), "terminals": s.terms.List()})
}

func (s *Server) startTerm(w http.ResponseWriter, r *http.Request) {
	if !s.termsOn.Load() {
		http.Error(w, "experimental terminals are off (⚙ Card layout → Experimental)", http.StatusForbidden)
		return
	}
	var sp term.Spec
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&sp); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	t, err := s.terms.Start(sp)
	if errors.Is(err, term.ErrUnsupported) {
		http.Error(w, err.Error(), http.StatusNotImplemented)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Printf("terminal %s: %s in %s (pid %d)", t.Info().ID, s.terms.Command, t.Info().Dir, t.Info().PID)
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, t.Info())
}

func (s *Server) stopTerm(w http.ResponseWriter, r *http.Request) {
	if err := s.terms.Stop(r.PathValue("id")); err != nil {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// guard() has already rejected foreign Hosts and Origins, so the upgrader accepts the rest.
var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

// attachTerm bridges one viewer to a terminal: the scrollback first, then live output as
// binary frames. From the viewer, binary frames are keystrokes and text frames are
// {"cols","rows"} resizes. A text frame {"exit":true} tells the viewer the process ended.
func (s *Server) attachTerm(w http.ResponseWriter, r *http.Request) {
	t, ok := s.terms.Get(r.PathValue("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	snap, out, detach := t.Attach()
	defer detach()
	if len(snap) > 0 && ws.WriteMessage(websocket.BinaryMessage, snap) != nil {
		return
	}
	go func() {
		for b := range out {
			if ws.WriteMessage(websocket.BinaryMessage, b) != nil {
				return
			}
		}
		if t.Info().Exited {
			_ = ws.WriteMessage(websocket.TextMessage, []byte(`{"exit":true}`))
		}
		_ = ws.Close() // dropped for lagging: the viewer reconnects and replays the scrollback
	}()
	for {
		kind, data, err := ws.ReadMessage()
		if err != nil {
			return
		}
		if kind == websocket.TextMessage {
			var sz struct{ Cols, Rows uint16 }
			if json.Unmarshal(data, &sz) == nil && sz.Cols > 0 && sz.Rows > 0 {
				_ = t.Resize(sz.Cols, sz.Rows)
			}
			continue
		}
		if t.Write(data) != nil {
			return
		}
	}
}
