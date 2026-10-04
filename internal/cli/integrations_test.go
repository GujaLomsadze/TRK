package cli

import (
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func runIn(stdin string, args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errb)
	return code, out.String()
}

func TestHookForwardsAndPrintsNothing(t *testing.T) {
	got := capture(t)
	code, out := runIn(`{"session_id":"A","hook_event_name":"Stop"}`, "hook")
	if code != 0 || out != "" || len(*got) != 1 || (*got)[0].Source != "hook" || (*got)[0].ClaudePID != 4242 {
		t.Fatalf("code=%d out=%q got=%+v", code, out, *got)
	}
}

func TestHookGarbageStdin(t *testing.T) {
	got := capture(t)
	for _, in := range []string{"", "not json", "{", strings.Repeat("x", 9<<20)} {
		if code, out := runIn(in, "hook"); code != 0 || out != "" {
			t.Fatalf("code=%d out=%q", code, out)
		}
	}
	if len(*got) != 0 {
		t.Fatalf("sent %d garbage events", len(*got))
	}
}

func TestHookDaemonDownIsFastAndSilent(t *testing.T) {
	t.Setenv("TRK_URL", "http://127.0.0.1:1") // nothing listens; TRK_URL disables spawn
	start := time.Now()
	var out, errb bytes.Buffer
	code := Run([]string{"hook"}, strings.NewReader(`{"session_id":"A"}`), &out, &errb)
	if code != 0 || out.Len() != 0 || errb.Len() != 0 || time.Since(start) > 500*time.Millisecond {
		t.Fatalf("code=%d out=%q err=%q took=%v", code, out.String(), errb.String(), time.Since(start))
	}
}

func TestCompactLine(t *testing.T) {
	future := time.Now().Add(time.Hour).Unix()
	cases := []struct{ in, want string }{
		{`{"context_window":{"used_percentage":42.4},"rate_limits":{"five_hour":{"used_percentage":23,"resets_at":` + itoa(future) + `},"seven_day":{"used_percentage":61.2}}}`, "trk · ctx 42% · 5h 23% · 7d 61%"},
		{`{"context_window":{"used_percentage":null}}`, "trk · ctx — · 5h — · 7d —"},
		{`{"rate_limits":{"five_hour":{"used_percentage":90,"resets_at":1}}}`, "trk · ctx — · 5h — · 7d —"},
		{`garbage`, "trk · ctx — · 5h — · 7d —"},
	}
	for _, c := range cases {
		if got := compactLine([]byte(c.in)); got != c.want {
			t.Errorf("compactLine(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestStatuslineGarbage(t *testing.T) {
	capture(t)
	code, out := runIn("not json", "statusline")
	if code != 0 || strings.TrimSpace(out) != "trk · ctx — · 5h — · 7d —" {
		t.Fatalf("code=%d out=%q", code, out)
	}
}

func TestStatuslineThenChains(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	got := capture(t)
	code, out := runIn(`{"session_id":"A","model":{"display_name":"Opus"}}`, "statusline", "--then", `cat | grep -o '"display_name":"[^"]*"'`)
	if code != 0 || strings.TrimSpace(out) != `"display_name":"Opus"` || len(*got) != 1 {
		t.Fatalf("code=%d out=%q sent=%d", code, out, len(*got))
	}
}

func itoa(n int64) string { return strconvFormat(n) }

func strconvFormat(n int64) string { return strconv.FormatInt(n, 10) }
