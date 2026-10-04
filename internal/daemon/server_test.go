package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GujaLomsadze/trk/internal/fleet"
	"github.com/GujaLomsadze/trk/internal/gitinfo"
	"github.com/GujaLomsadze/trk/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "trk.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := NewServer(st, fleet.New(gitinfo.Lookup), log.New(io.Discard, "", 0))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go s.Loop(ctx)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

func post(t *testing.T, url, body string, hdr map[string]string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", url+"/v1/events", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

const permEvent = `{"source":"hook","payload":{"session_id":"S1","hook_event_name":"PermissionRequest","cwd":"/tmp/proj","tool_name":"Bash","tool_input":{"command":"npm test"}}}`

func TestPostAndQuery(t *testing.T) {
	ts, _ := newTestServer(t)
	if c := post(t, ts.URL, permEvent, nil); c != http.StatusAccepted {
		t.Fatalf("post = %d", c)
	}
	var v fleet.View
	getJSON(t, ts.URL+"/v1/sessions", &v)
	if len(v.Sessions) != 1 || v.Sessions[0].Status != "waiting" || len(v.Needs) != 1 {
		t.Fatalf("view = %+v", v)
	}
	var detail struct {
		Session fleet.SessionView `json:"session"`
		Events  []json.RawMessage `json:"events"`
	}
	getJSON(t, ts.URL+"/v1/sessions/S1", &detail)
	if detail.Session.SessionID != "S1" || len(detail.Events) != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	resp, _ := http.Get(ts.URL + "/v1/sessions/nope")
	if resp.StatusCode != 404 {
		t.Fatalf("missing session = %d", resp.StatusCode)
	}
}

func TestBadAndDuplicate(t *testing.T) {
	ts, _ := newTestServer(t)
	if c := post(t, ts.URL, `{not json`, nil); c != 400 {
		t.Fatalf("bad json = %d", c)
	}
	sl := `{"source":"statusline","payload":{"session_id":"S1","context_window":{"used_percentage":12}}}`
	if c := post(t, ts.URL, sl, nil); c != 202 {
		t.Fatalf("first status = %d", c)
	}
	if c := post(t, ts.URL, sl, nil); c != 204 {
		t.Fatalf("duplicate status = %d", c)
	}
}

func TestGuard(t *testing.T) {
	ts, _ := newTestServer(t)
	cases := []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"cli (no origin)", nil, 202},
		{"dashboard origin", map[string]string{"Origin": "http://localhost:7777"}, 202},
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, 403},
		{"null origin", map[string]string{"Origin": "null"}, 403},
	}
	for _, c := range cases {
		if got := post(t, ts.URL, permEvent, c.hdr); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	req, _ := http.NewRequest("GET", ts.URL+"/v1/sessions", nil)
	req.Host = "attacker.example:7777" // DNS rebinding
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 403 {
		t.Fatalf("rebinding host: %v %v", resp.StatusCode, err)
	}
}

func TestHealthzAndIndex(t *testing.T) {
	ts, _ := newTestServer(t)
	var h map[string]any
	getJSON(t, ts.URL+"/healthz", &h)
	if h["app"] != "trk" || h["ok"] != true {
		t.Fatalf("healthz = %v", h)
	}
	resp, _ := http.Get(ts.URL + "/")
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(b), "TRK.EXE") {
		t.Fatalf("index = %d %q", resp.StatusCode, b)
	}
}

func TestStreamDeliversWithinOneSecond(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	frames := make(chan string, 8)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if l := sc.Text(); strings.HasPrefix(l, "data: ") {
				frames <- l[6:]
			}
		}
	}()
	select {
	case <-frames: // initial snapshot
	case <-time.After(time.Second):
		t.Fatal("no initial snapshot")
	}
	start := time.Now()
	post(t, ts.URL, permEvent, nil)
	for {
		select {
		case f := <-frames:
			if strings.Contains(f, `"needs_you":1`) {
				if d := time.Since(start); d > time.Second {
					t.Fatalf("took %v", d)
				}
				return
			}
		case <-time.After(time.Second):
			t.Fatal("no update within 1s")
		}
	}
}

func getJSON(t *testing.T, url string, v any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestShutdownEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	called := make(chan struct{}, 1)
	s.OnShutdown = func() { called <- struct{}{} }
	req, _ := http.NewRequest("POST", ts.URL+"/v1/shutdown", nil)
	req.Header.Set("Origin", "https://evil.example")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Fatalf("foreign origin shutdown = %d", resp.StatusCode)
	}
	resp, err := http.Post(ts.URL+"/v1/shutdown", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("shutdown = %v %v", resp, err)
	}
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("OnShutdown not called")
	}
}
