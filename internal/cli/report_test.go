package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GujaLomsadze/trk/internal/model"
)

func capture(t *testing.T) *[]model.Envelope {
	t.Helper()
	var got []model.Envelope
	oldSend, oldFind, oldNow := send, findClaude, nowMS
	send = func(e model.Envelope) bool { got = append(got, e); return true }
	findClaude = func() int { return 4242 }
	nowMS = func() int64 { return 1700000000000 }
	t.Cleanup(func() { send, findClaude, nowMS = oldSend, oldFind, oldNow })
	return &got
}

func run(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

func TestParseStart(t *testing.T) {
	cases := []struct {
		args  []string
		text  string
		steps int
	}{
		{[]string{"Refactor Kafka consumer", "--steps", "5"}, "Refactor Kafka consumer", 5},
		{[]string{"--steps=3", "Fix", "bug"}, "Fix bug", 3},
		{[]string{"No steps"}, "No steps", 0},
		{[]string{"x", "--steps", "nope"}, "x", 0},
	}
	for _, c := range cases {
		text, steps := parseStart(c.args)
		if text != c.text || steps != c.steps {
			t.Errorf("parseStart(%q) = %q,%d", c.args, text, steps)
		}
	}
}

func TestParseProgress(t *testing.T) {
	cases := map[string][3]int{"3/5": {3, 5, 1}, "3": {3, 0, 1}, " 2 / 4 ": {2, 4, 1}, "x/5": {0, 0, 0}, "": {0, 0, 0}, "-1/5": {0, 0, 0}}
	for in, w := range cases {
		i, n, ok := parseProgress(in)
		if i != w[0] || n != w[1] || ok != (w[2] == 1) {
			t.Errorf("parseProgress(%q) = %d,%d,%v", in, i, n, ok)
		}
	}
}

func TestReportingVerbs(t *testing.T) {
	got := capture(t)
	t.Setenv("TRK_SESSION", "")
	for _, args := range [][]string{
		{"start", "Refactor", "--steps", "5"}, {"step", "Writing tests"}, {"progress", "3/5"},
		{"blocked", "Drop legacy topic?"}, {"done", "Tests green"},
	} {
		if code, out, _ := run(args...); code != 0 || out != "" {
			t.Fatalf("%v: code=%d out=%q", args, code, out)
		}
	}
	if len(*got) != 5 {
		t.Fatalf("sent %d", len(*got))
	}
	e := (*got)[2]
	var p map[string]any
	json.Unmarshal(e.Payload, &p)
	if e.Kind != "progress" || e.Source != "cli" || e.ClaudePID != 4242 || p["i"] != 3.0 || p["n"] != 5.0 || p["cwd"] == "" {
		t.Fatalf("progress envelope = %+v %v", e, p)
	}
}

func TestTrkSessionEnv(t *testing.T) {
	got := capture(t)
	t.Setenv("TRK_SESSION", "my-sess")
	run("step", "x")
	if e := (*got)[0]; e.SessionID != "my-sess" || e.SessionVia != "env" {
		t.Fatalf("envelope = %+v", e)
	}
}

func TestBadArgsStillExitZero(t *testing.T) {
	got := capture(t)
	for _, args := range [][]string{{"progress", "banana"}, {"step"}, {"start"}} {
		if code, _, _ := run(args...); code != 0 {
			t.Fatalf("%v exit %d", args, code)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("sent %d for bad args", len(*got))
	}
}

func TestPanicInSendStillExitZero(t *testing.T) {
	capture(t)
	send = func(model.Envelope) bool { panic("boom") }
	if code, _, _ := run("step", "x"); code != 0 {
		t.Fatalf("exit %d", code)
	}
}
