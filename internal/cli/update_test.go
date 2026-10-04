package cli

import (
	"strings"
	"testing"
)

func TestStaleDaemon(t *testing.T) {
	cases := []struct {
		running, own string
		want         bool
	}{
		{"0.1.0", "0.1.1", true},
		{"0.1.1", "0.1.1", false},
		{"", "0.1.1", false}, // nothing running
	}
	for _, c := range cases {
		if got := staleDaemon(c.running, c.own); got != c.want {
			t.Errorf("staleDaemon(%q,%q) = %v", c.running, c.own, got)
		}
	}
}

func TestUpdateOnSourceBuild(t *testing.T) {
	code, out, _ := run("update") // tests run as version "dev"
	if code != 0 || !strings.Contains(out, "git pull") {
		t.Fatalf("code=%d out=%q", code, out)
	}
}
