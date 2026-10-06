//go:build !windows

package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func postJSON(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestTerminals(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.terms.Command = "cat"
	t.Cleanup(srv.CloseTerminals)
	dir := t.TempDir()
	spec := `{"dir":` + jsonStr(dir) + `}`

	if r := postJSON(t, ts.URL+"/v1/terms", spec); r.StatusCode != http.StatusForbidden {
		t.Fatalf("start while off = %d, want 403", r.StatusCode)
	}
	if r := postJSON(t, ts.URL+"/v1/settings", `{"terminals_enabled":true}`); r.StatusCode != http.StatusNoContent {
		t.Fatalf("enable = %d", r.StatusCode)
	}
	var view struct {
		On bool `json:"terminals_enabled"`
	}
	getJSON(t, ts.URL+"/v1/sessions", &view)
	if !view.On {
		t.Fatal("snapshot does not report terminals on")
	}
	if r := postJSON(t, ts.URL+"/v1/terms", `{"dir":"/no/such/dir"}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad dir = %d", r.StatusCode)
	}
	r := postJSON(t, ts.URL+"/v1/terms", spec)
	if r.StatusCode != http.StatusCreated {
		t.Fatalf("start = %d", r.StatusCode)
	}
	var info struct{ ID string }
	_ = json.NewDecoder(r.Body).Decode(&info)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/terms/" + info.ID + "/ws"
	if _, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": {"https://evil.example"}}); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin attach allowed: %v", err)
	}
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.WriteMessage(websocket.TextMessage, []byte(`{"cols":100,"rows":30}`))
	_ = c.WriteMessage(websocket.BinaryMessage, []byte("hello-ws\n"))
	var got bytes.Buffer
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for bytes.Count(got.Bytes(), []byte("hello-ws")) < 2 {
		_, b, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v (got %q)", err, got.String())
		}
		got.Write(b)
	}
	// a second viewer starts with the scrollback
	c2, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = c2.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, b, err := c2.ReadMessage(); err != nil || !bytes.Contains(b, []byte("hello-ws")) {
		t.Fatalf("second viewer scrollback = %q, %v", b, err)
	}
	c2.Close()

	var list struct{ Running int }
	getJSON(t, ts.URL+"/v1/terms", &list)
	if list.Running != 1 {
		t.Fatalf("running = %d", list.Running)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/terms/"+info.ID, nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("stop: %v", err)
	}
	getJSON(t, ts.URL+"/v1/terms", &list)
	if list.Running != 0 {
		t.Fatalf("running after stop = %d", list.Running)
	}
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		if _, _, err := c.ReadMessage(); err != nil {
			break // viewer is disconnected once the agent is stopped
		}
	}
}

// A terminal is linked to the session whose claude runs in it; a session that is still
// running can be forked but not resumed a second time.
func TestTerminalLinksSessionAndGuardsResume(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("needs /proc to see the fake claude")
	}
	ts, srv := newTestServer(t)
	t.Cleanup(srv.CloseTerminals)
	// ptree recognises claude by process name; a script's process is named after the script.
	// (Not a symlink to cat: multicall coreutils exit when called by an unknown name.)
	fake := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nwhile read -r l; do echo \"$l\"; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv.terms.Command = fake
	dir := t.TempDir()
	postJSON(t, ts.URL+"/v1/settings", `{"terminals_enabled":true}`)
	start := func(extra string) *http.Response {
		return postJSON(t, ts.URL+"/v1/terms", `{"dir":`+jsonStr(dir)+extra+`}`)
	}
	r := start("")
	var first struct {
		ID  string
		PID int
	}
	_ = json.NewDecoder(r.Body).Decode(&first)
	post(t, ts.URL, fmt.Sprintf(`{"source":"hook","claude_pid":%d,"payload":{"session_id":"S-TRK","hook_event_name":"UserPromptSubmit","cwd":%s,"prompt":"hi"}}`, first.PID, jsonStr(dir)), nil)

	var view struct {
		Running   map[string]string `json:"running"`
		Terminals []struct {
			ID        string `json:"id"`
			SessionID string `json:"session_id"`
		} `json:"terminals"`
	}
	linked := func() bool {
		getJSON(t, ts.URL+"/v1/sessions", &view)
		return view.Running["S-TRK"] == first.ID && len(view.Terminals) == 1 && view.Terminals[0].SessionID == "S-TRK"
	}
	waitFor(t, linked, "terminal linked to S-TRK") // events are ingested asynchronously
	if r := start(`,"resume":"S-TRK"`); r.StatusCode != http.StatusConflict {
		t.Fatalf("resume of a running session = %d, want 409", r.StatusCode)
	}
	if r := start(`,"resume":"S-TRK","fork":true`); r.StatusCode != http.StatusCreated {
		t.Fatalf("fork = %d", r.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/v1/terms/"+first.ID, nil)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("stop: %v", err)
	}
	// the killed process may linger for a moment
	waitFor(t, func() bool { return start(`,"resume":"S-TRK"`).StatusCode == http.StatusCreated }, "resume after stop")
}

func waitFor(t *testing.T, ok func() bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }
