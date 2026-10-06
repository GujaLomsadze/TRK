package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
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

func TestStopEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	stopped := make(chan struct{}, 1)
	s.OnStop = func() { stopped <- struct{}{} }
	req, _ := http.NewRequest("POST", ts.URL+"/v1/stop", nil)
	req.Header.Set("Origin", "https://evil.example")
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 403 {
		t.Fatalf("foreign origin stop = %d", resp.StatusCode)
	}
	req, _ = http.NewRequest("POST", ts.URL+"/v1/stop", nil)
	req.Header.Set("Origin", "http://localhost:7777") // the dashboard's own button
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusAccepted {
		t.Fatalf("stop = %v %v", resp, err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("OnStop not called")
	}
}

func TestLimitHistory(t *testing.T) {
	ts, srv := newTestServer(t)
	const t0 = int64(1_791_000_000_000) // a whole minute
	var clock atomic.Int64
	srv.now = func() int64 { return clock.Load() }
	reading := func(at int64, five float64, fiveReset int64) {
		t.Helper()
		clock.Store(at)
		body := fmt.Sprintf(`{"source":"statusline","payload":{"session_id":"S1","rate_limits":{`+
			`"five_hour":{"used_percentage":%v,"resets_at":%d},"seven_day":{"used_percentage":%v,"resets_at":%d}}}}`,
			five, fiveReset, five/4, t0/1000+86400)
		if c := post(t, ts.URL, body, nil); c != 202 {
			t.Fatalf("post = %d", c)
		}
	}
	reset := t0/1000 + 3600                  // window runs t0-4h .. t0+1h
	reading(t0-5*3600_000, 80, t0/1000-3600) // previous window: excluded
	reading(t0-120_000, 10, reset)
	reading(t0, 12, reset)
	reading(t0+20_000, 15, reset) // same minute as t0: highest wins

	var got struct {
		Window   string             `json:"window"`
		Start    int64              `json:"start"`
		ResetsAt int64              `json:"resets_at"`
		Now      int64              `json:"now"`
		Points   []store.LimitPoint `json:"points"`
	}
	getJSON(t, ts.URL+"/v1/limits/history?window=5h", &got)
	if got.ResetsAt != reset*1000 || got.Start != reset*1000-5*3600_000 || got.Now != t0+20_000 {
		t.Fatalf("window = %+v", got)
	}
	want := []store.LimitPoint{{TS: t0 - 120_000, Pct: 10}, {TS: t0 + 20_000, Pct: 15}}
	if len(got.Points) != 2 || got.Points[0] != want[0] || got.Points[1] != want[1] {
		t.Fatalf("points = %+v, want %+v", got.Points, want)
	}
	getJSON(t, ts.URL+"/v1/limits/history?window=7d", &got)
	if got.Window != "7d" || got.Start != (t0/1000+86400)*1000-7*86400_000 || len(got.Points) != 3 {
		t.Fatalf("7d = %+v", got)
	}
	resp, _ := http.Get(ts.URL + "/v1/limits/history?window=1y")
	if resp.StatusCode != 400 {
		t.Fatalf("bad window = %d", resp.StatusCode)
	}
}

func TestDismissEndpoint(t *testing.T) {
	ts, _ := newTestServer(t)
	post(t, ts.URL, `{"source":"hook","payload":{"session_id":"D","hook_event_name":"Stop","cwd":"/tmp/d"}}`, nil)
	post(t, ts.URL, `{"source":"hook","payload":{"session_id":"W","hook_event_name":"PreToolUse","cwd":"/tmp/w","tool_name":"Bash","tool_input":{"command":"ls"}}}`, nil)
	code := func(id string) int {
		resp, err := http.Post(ts.URL+"/v1/sessions/"+id+"/dismiss", "application/json", nil)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if c := code("W"); c != http.StatusConflict {
		t.Fatalf("working session dismiss = %d", c)
	}
	if c := code("nope"); c != http.StatusNotFound {
		t.Fatalf("unknown = %d", c)
	}
	if c := code("D"); c != http.StatusNoContent {
		t.Fatalf("done session dismiss = %d", c)
	}
	var v fleet.View
	getJSON(t, ts.URL+"/v1/sessions", &v)
	if len(v.Sessions) != 1 || v.Sessions[0].SessionID != "W" {
		t.Fatalf("after dismiss: %+v", v.Sessions)
	}
}

func TestSettingsEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	resp, _ := http.Post(ts.URL+"/v1/settings", "application/json", strings.NewReader(`{"hide_after_min":60}`))
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("settings = %d", resp.StatusCode)
	}
	var v fleet.View
	getJSON(t, ts.URL+"/v1/sessions", &v)
	if v.HideAfterMin != 60 {
		t.Fatalf("hide_after_min = %d", v.HideAfterMin)
	}
	if val, ok, _ := s.st.Setting("hide_after_min"); !ok || val != "60" {
		t.Fatalf("not persisted: %q %v", val, ok)
	}
	for _, bad := range []string{`{"hide_after_min":-5}`, `{"hide_after_min":"x"}`, `nope`} {
		resp, _ := http.Post(ts.URL+"/v1/settings", "application/json", strings.NewReader(bad))
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s → %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestTitleEndpoint(t *testing.T) {
	ts, s := newTestServer(t)
	post(t, ts.URL, `{"source":"hook","payload":{"session_id":"A","hook_event_name":"Stop","cwd":"/tmp/proj"}}`, nil)
	post(t, ts.URL, `{"source":"hook","payload":{"session_id":"B","hook_event_name":"Stop","cwd":"/tmp/proj"}}`, nil)
	title := func(id, body string) int {
		resp, err := http.Post(ts.URL+"/v1/sessions/"+id+"/title", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	var v fleet.View
	for deadline := time.Now().Add(3 * time.Second); len(v.Sessions) < 2; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("sessions never appeared")
		}
		getJSON(t, ts.URL+"/v1/sessions", &v)
	}
	if c := title("A", `{"title":"  Parquet\tbug  hunt \n"}`); c != http.StatusNoContent {
		t.Fatalf("set title = %d", c)
	}
	for _, bad := range []struct{ id, body string }{{"A", `{}`}, {"A", `{"title":"` + strings.Repeat("x", 81) + `"}`}} {
		if c := title(bad.id, bad.body); c != http.StatusBadRequest {
			t.Errorf("%s %s = %d, want 400", bad.id, bad.body, c)
		}
	}
	if c := title("nope", `{"title":"x"}`); c != http.StatusNotFound {
		t.Fatalf("unknown = %d", c)
	}
	getJSON(t, ts.URL+"/v1/sessions", &v)
	names := map[string]fleet.SessionView{}
	for _, sv := range v.Sessions {
		names[sv.SessionID] = sv
	}
	// the titled card shows its title; the other no longer collides, so it loses its #suffix
	if a := names["A"]; a.Name != "Parquet bug hunt" || a.Title != "Parquet bug hunt" || a.AutoName != "proj" {
		t.Fatalf("A = %q title %q auto %q", a.Name, a.Title, a.AutoName)
	}
	if b := names["B"]; b.Name != "proj" {
		t.Fatalf("B = %q", b.Name)
	}
	rows, _ := s.st.Sessions()
	for _, r := range rows {
		if r.SessionID == "A" && r.Title != "Parquet bug hunt" {
			t.Fatalf("title not persisted: %+v", r)
		}
	}
	if c := title("A", `{"title":""}`); c != http.StatusNoContent {
		t.Fatalf("clear = %d", c)
	}
	var after fleet.View // fresh: decoding into v would keep its old omitempty fields
	getJSON(t, ts.URL+"/v1/sessions", &after)
	for _, sv := range after.Sessions {
		if sv.SessionID == "A" && (sv.Title != "" || !strings.HasPrefix(sv.Name, "proj")) {
			t.Fatalf("after clear: %+v", sv)
		}
	}
}
