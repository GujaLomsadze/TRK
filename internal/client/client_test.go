package client

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/GujaLomsadze/trk/internal/model"
)

func deadURL(t *testing.T) string {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	u := "http://" + l.Addr().String()
	l.Close()
	return u
}

func TestSendDelivers(t *testing.T) {
	var got atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/events" && r.Method == "POST" {
			got.Add(1)
		}
		w.WriteHeader(202)
	}))
	defer ts.Close()
	c := &Client{BaseURL: ts.URL, HTTP: &http.Client{Timeout: 200 * time.Millisecond}}
	if !c.Send(model.Envelope{Source: "cli", Kind: "step"}) || got.Load() != 1 {
		t.Fatal("not delivered")
	}
}

func TestSendDownNoSpawnIsFastAndSilent(t *testing.T) {
	c := &Client{BaseURL: deadURL(t), HTTP: &http.Client{Timeout: 200 * time.Millisecond}}
	start := time.Now()
	if c.Send(model.Envelope{Source: "cli"}) {
		t.Fatal("reported delivered")
	}
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Fatalf("took %v", d)
	}
}

func TestSendHangingServerRespectsTimeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(2 * time.Second) }))
	defer ts.Close()
	var spawned atomic.Int32
	c := &Client{BaseURL: ts.URL, HTTP: &http.Client{Timeout: 200 * time.Millisecond}, AllowSpawn: true,
		Spawn: func() error { spawned.Add(1); return nil }, SpawnBudget: time.Second}
	start := time.Now()
	c.Send(model.Envelope{Source: "cli"})
	if d := time.Since(start); d > 400*time.Millisecond || spawned.Load() != 0 {
		t.Fatalf("took %v, spawned %d (timeout must not spawn)", d, spawned.Load())
	}
}

func TestSendSpawnsThenDelivers(t *testing.T) {
	url := deadURL(t)
	addr := url[len("http://"):]
	var spawned atomic.Int32
	var srv *http.Server
	c := &Client{BaseURL: url, HTTP: &http.Client{Timeout: 200 * time.Millisecond}, AllowSpawn: true, SpawnBudget: time.Second,
		Spawn: func() error {
			spawned.Add(1)
			go func() {
				time.Sleep(150 * time.Millisecond) // daemon boot time
				l, err := net.Listen("tcp", addr)
				if err != nil {
					return
				}
				srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })}
				srv.Serve(l)
			}()
			return nil
		}}
	if !c.Send(model.Envelope{Source: "cli"}) || spawned.Load() != 1 {
		t.Fatalf("spawned=%d", spawned.Load())
	}
	if srv != nil {
		srv.Close()
	}
}

func TestVersionAndShutdown(t *testing.T) {
	var stopped atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/healthz":
			w.Write([]byte(`{"ok":true,"app":"trk","version":"0.1.0"}`))
		case "/v1/shutdown":
			stopped.Add(1)
			w.WriteHeader(202)
		}
	}))
	defer ts.Close()
	c := &Client{BaseURL: ts.URL, HTTP: &http.Client{Timeout: 200 * time.Millisecond}}
	if v := c.DaemonVersion(); v != "0.1.0" {
		t.Fatalf("DaemonVersion = %q", v)
	}
	if !c.Shutdown() || stopped.Load() != 1 {
		t.Fatal("shutdown not sent")
	}
	down := &Client{BaseURL: deadURL(t), HTTP: &http.Client{Timeout: 200 * time.Millisecond}}
	if v := down.DaemonVersion(); v != "" {
		t.Fatalf("down daemon version = %q", v)
	}
}

func TestPausedSendsNothingAndNeverSpawns(t *testing.T) {
	var hits, spawned atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(202) }))
	defer ts.Close()
	c := &Client{BaseURL: ts.URL, HTTP: &http.Client{Timeout: 200 * time.Millisecond}, Paused: true,
		AllowSpawn: true, Spawn: func() error { spawned.Add(1); return nil }, SpawnBudget: time.Second}
	if c.Send(model.Envelope{Source: "hook"}) || hits.Load() != 0 || spawned.Load() != 0 {
		t.Fatalf("paused client sent=%d spawned=%d", hits.Load(), spawned.Load())
	}
}

func TestDashboardAgents(t *testing.T) {
	for _, c := range []struct {
		status int
		body   string
		want   int
	}{{200, `{"running":2,"terminals":[]}`, 2}, {404, `404 page not found`, 0}, {200, `garbage`, 0}} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		}))
		cl := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
		if got := cl.DashboardAgents(); got != c.want {
			t.Errorf("%d %q: got %d, want %d", c.status, c.body, got, c.want)
		}
		srv.Close()
	}
}
