//go:build !windows

package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
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

func TestExperimentalTerminals(t *testing.T) {
	ts, srv := newTestServer(t)
	srv.terms.Command = "cat"
	t.Cleanup(srv.CloseTerminals)
	dir := t.TempDir()
	spec := `{"dir":` + jsonStr(dir) + `}`

	if r := postJSON(t, ts.URL+"/v1/terms", spec); r.StatusCode != http.StatusForbidden {
		t.Fatalf("start while off = %d, want 403", r.StatusCode)
	}
	if r := postJSON(t, ts.URL+"/v1/settings", `{"experimental_terminals":true}`); r.StatusCode != http.StatusNoContent {
		t.Fatalf("enable = %d", r.StatusCode)
	}
	var view struct {
		On bool `json:"experimental_terminals"`
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

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }
