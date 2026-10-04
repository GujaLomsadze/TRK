package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	var out, errb bytes.Buffer
	code := Run([]string{"version"}, strings.NewReader(""), &out, &errb)
	if code != 0 || !strings.HasPrefix(out.String(), "trk ") {
		t.Fatalf("code=%d out=%q", code, out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if code := Run([]string{"frobnicate"}, strings.NewReader(""), &out, &errb); code != 2 {
		t.Fatalf("code=%d, want 2", code)
	}
}
