package config

import "testing"

func TestBaseURL(t *testing.T) {
	cases := []struct {
		name, port, url, want string
		local                 bool
	}{
		{"default", "", "", "http://127.0.0.1:7777", true},
		{"port", "9000", "", "http://127.0.0.1:9000", true},
		{"bad port", "nope", "", "http://127.0.0.1:7777", true},
		{"out of range", "70000", "", "http://127.0.0.1:7777", true},
		{"url override", "9000", "http://host.docker.internal:7777/", "http://host.docker.internal:7777", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TRK_PORT", c.port)
			t.Setenv("TRK_URL", c.url)
			if got := BaseURL(); got != c.want {
				t.Fatalf("BaseURL() = %q, want %q", got, c.want)
			}
			if got := IsLocalDefault(); got != c.local {
				t.Fatalf("IsLocalDefault() = %v, want %v", got, c.local)
			}
		})
	}
}

func TestDataDirOverride(t *testing.T) {
	t.Setenv("TRK_DATA_DIR", "/tmp/trk-x")
	got, err := DataDir()
	if err != nil || got != "/tmp/trk-x" {
		t.Fatalf("DataDir() = %q, %v", got, err)
	}
}
