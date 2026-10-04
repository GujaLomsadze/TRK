package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, latest string
		want        bool
	}{
		{"0.1.0", "v0.1.1", true},
		{"0.1.0", "v0.2.0", true},
		{"0.9.0", "v0.10.0", true},
		{"0.1.1", "v0.1.1", false},
		{"0.2.0", "v0.1.9", false},
		{"dev", "v9.9.9", false},                // source builds never nag
		{"0.0.0-SNAPSHOT-abc", "v0.1.0", false}, // snapshots neither
		{"0.1.0", "garbage", false},
		{"v1.2.3", "v1.2.4", true},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.latest, got, c.want)
		}
	}
}

func TestLatestFollowsRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/owner/repo/releases/latest" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/owner/repo/releases/tag/v0.3.1", http.StatusFound)
	}))
	defer srv.Close()
	u := &Updater{Base: srv.URL, Repo: "owner/repo", HTTP: srv.Client()}
	tag, err := u.Latest(context.Background())
	if err != nil || tag != "v0.3.1" {
		t.Fatalf("Latest = %q, %v", tag, err)
	}
}

// fakeRelease serves an archive holding a "trk" binary with the given body, plus checksums.txt.
func fakeRelease(t *testing.T, body []byte, corrupt bool) *httptest.Server {
	t.Helper()
	name := AssetName(runtime.GOOS, runtime.GOARCH)
	var archive bytes.Buffer
	bin := "trk"
	if runtime.GOOS == "windows" {
		bin = "trk.exe"
		zw := zip.NewWriter(&archive)
		f, _ := zw.Create(bin)
		f.Write(body)
		zw.Close()
	} else {
		gz := gzip.NewWriter(&archive)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: "README.md", Mode: 0o644, Size: 2})
		tw.Write([]byte("hi"))
		tw.WriteHeader(&tar.Header{Name: bin, Mode: 0o755, Size: int64(len(body))})
		tw.Write(body)
		tw.Close()
		gz.Close()
	}
	sum := sha256.Sum256(archive.Bytes())
	hexsum := hex.EncodeToString(sum[:])
	if corrupt {
		hexsum = hex.EncodeToString(make([]byte, 32))
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/o/r/releases/download/v0.2.0/" + name:
			w.Write(archive.Bytes())
		case "/o/r/releases/download/v0.2.0/checksums.txt":
			fmt.Fprintf(w, "%s  %s\n%s  other.tar.gz\n", hexsum, name, hex.EncodeToString(make([]byte, 32)))
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestApplyReplacesBinary(t *testing.T) {
	srv := fakeRelease(t, []byte("NEW BINARY"), false)
	defer srv.Close()
	exe := filepath.Join(t.TempDir(), "trk")
	os.WriteFile(exe, []byte("OLD BINARY"), 0o755)
	u := &Updater{Base: srv.URL, Repo: "o/r", HTTP: &http.Client{Timeout: 5 * time.Second}}
	if err := u.Apply(context.Background(), "v0.2.0", exe); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "NEW BINARY" {
		t.Fatalf("exe = %q", got)
	}
	if st, _ := os.Stat(exe); runtime.GOOS != "windows" && st.Mode().Perm()&0o100 == 0 {
		t.Fatal("new binary not executable")
	}
}

func TestApplyRejectsBadChecksum(t *testing.T) {
	srv := fakeRelease(t, []byte("EVIL"), true)
	defer srv.Close()
	exe := filepath.Join(t.TempDir(), "trk")
	os.WriteFile(exe, []byte("OLD BINARY"), 0o755)
	u := &Updater{Base: srv.URL, Repo: "o/r", HTTP: &http.Client{Timeout: 5 * time.Second}}
	if err := u.Apply(context.Background(), "v0.2.0", exe); err == nil {
		t.Fatal("accepted a checksum mismatch")
	}
	got, _ := os.ReadFile(exe)
	if string(got) != "OLD BINARY" {
		t.Fatalf("original binary touched: %q", got)
	}
	left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), "*"))
	if len(left) != 1 {
		t.Fatalf("leftover files: %v", left)
	}
}

func TestCheckIsCachedForADay(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Redirect(w, r, "/o/r/releases/tag/v0.5.0", http.StatusFound)
	}))
	defer srv.Close()
	dir := t.TempDir()
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	u := &Updater{Base: srv.URL, Repo: "o/r", HTTP: srv.Client(), CacheDir: dir, Now: func() time.Time { return now }}
	for i := 0; i < 3; i++ {
		if tag, err := u.CachedLatest(context.Background()); err != nil || tag != "v0.5.0" {
			t.Fatalf("CachedLatest = %q, %v", tag, err)
		}
	}
	if calls != 1 {
		t.Fatalf("network calls = %d, want 1", calls)
	}
	now = now.Add(25 * time.Hour)
	u.CachedLatest(context.Background())
	if calls != 2 {
		t.Fatalf("after 25h calls = %d, want 2", calls)
	}
}
