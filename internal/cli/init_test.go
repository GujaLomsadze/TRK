package cli

import (
	"os"
	"testing"
)

func TestIsTTYRejectsDevNull(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Skip(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Fatal("/dev/null treated as an interactive terminal")
	}
}
