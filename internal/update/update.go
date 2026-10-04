// Package update checks GitHub for a newer trk release and swaps the binary in place.
// Only `trk open` and `trk update` use it; hooks and the daemon never go online.
package update

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBase = "https://github.com"
	DefaultRepo = "GujaLomsadze/trk"
	checkEvery  = 24 * time.Hour
	maxArchive  = 64 << 20
)

type Updater struct {
	Base     string // https://github.com
	Repo     string // owner/name
	HTTP     *http.Client
	CacheDir string // where the daily check result is remembered; "" = no cache
	Now      func() time.Time
}

func Default(cacheDir string) *Updater {
	return &Updater{Base: DefaultBase, Repo: DefaultRepo, HTTP: &http.Client{Timeout: 20 * time.Second}, CacheDir: cacheDir}
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func AssetName(goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("trk_%s_%s%s", goos, goarch, ext)
}

// Newer reports whether latest is a newer release than current. Source and
// snapshot builds (non-plain versions) never report updates.
func Newer(current, latest string) bool {
	c, ok1 := parse(current)
	l, ok2 := parse(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := range c {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// ParseVersion parses a plain release version like 0.1.0 or v0.1.0.
func ParseVersion(v string) ([3]int, bool) { return parse(v) }

func parse(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// Latest asks GitHub which tag "latest" points to, via the releases/latest
// redirect (no API token, no rate-limited API call).
func (u *Updater) Latest(ctx context.Context) (string, error) {
	c := *u.HTTP
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.Base+"/"+u.Repo+"/releases/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode/100 != 3 || !strings.Contains(loc, "/releases/tag/") {
		return "", fmt.Errorf("no release found (HTTP %d)", resp.StatusCode)
	}
	return path.Base(loc), nil
}

type cache struct {
	CheckedAt time.Time `json:"checked_at"`
	Latest    string    `json:"latest"`
}

// CachedLatest is Latest, asked at most once a day.
func (u *Updater) CachedLatest(ctx context.Context) (string, error) {
	file := ""
	if u.CacheDir != "" {
		file = filepath.Join(u.CacheDir, "update-check.json")
		var c cache
		if b, err := os.ReadFile(file); err == nil && json.Unmarshal(b, &c) == nil &&
			c.Latest != "" && u.now().Sub(c.CheckedAt) < checkEvery {
			return c.Latest, nil
		}
	}
	tag, err := u.Latest(ctx)
	if err != nil {
		return "", err
	}
	if file != "" {
		b, _ := json.Marshal(cache{CheckedAt: u.now(), Latest: tag})
		_ = os.MkdirAll(u.CacheDir, 0o755)
		_ = os.WriteFile(file, b, 0o644)
	}
	return tag, nil
}

func (u *Updater) get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := u.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", path.Base(url), resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxArchive))
}

// Apply downloads release tag for this OS/arch, verifies its checksum and
// atomically replaces exe. On any error exe is left untouched.
func (u *Updater) Apply(ctx context.Context, tag, exe string) error {
	name := AssetName(runtime.GOOS, runtime.GOARCH)
	base := u.Base + "/" + u.Repo + "/releases/download/" + tag + "/"
	archive, err := u.get(ctx, base+name)
	if err != nil {
		return err
	}
	sums, err := u.get(ctx, base+"checksums.txt")
	if err != nil {
		return err
	}
	if err := verify(archive, sums, name); err != nil {
		return err
	}
	bin, err := extract(archive, runtime.GOOS == "windows")
	if err != nil {
		return err
	}
	return swap(exe, bin)
}

func verify(archive, sums []byte, name string) error {
	got := sha256.Sum256(archive)
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && f[1] == name {
			if f[0] == hex.EncodeToString(got[:]) {
				return nil
			}
			return errors.New("checksum mismatch: download is corrupt or tampered with; nothing was changed")
		}
	}
	return fmt.Errorf("%s not listed in checksums.txt", name)
}

func extract(archive []byte, isZip bool) ([]byte, error) {
	if isZip {
		zr, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
		if err != nil {
			return nil, err
		}
		for _, f := range zr.File {
			if path.Base(f.Name) == "trk.exe" {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, maxArchive))
			}
		}
		return nil, errors.New("trk.exe not found in archive")
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("trk not found in archive")
		}
		if err != nil {
			return nil, err
		}
		if path.Base(h.Name) == "trk" && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxArchive))
		}
	}
}

// swap writes the new binary next to exe and renames it into place. A running
// binary can be replaced by rename on Unix; Windows needs the old one moved aside first.
func swap(exe string, bin []byte) error {
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".trk-update-*")
	if err != nil {
		return fmt.Errorf("can't write next to %s: %w", exe, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(bin); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	return os.Rename(tmp.Name(), exe)
}
