package daemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GujaLomsadze/trk/internal/fleet"
	"github.com/GujaLomsadze/trk/internal/gitinfo"
	"github.com/GujaLomsadze/trk/internal/store"
)

var ErrAlreadyRunning = errors.New("trk daemon already running")

type Options struct {
	MaxDBBytes int64 // trim oldest events past this size; 0 = no cap
	Addr       string
	DataDir    string
	Logger     *log.Logger
	OnListen   func(addr string)
}

// Run binds first (the port is the single-instance lock), then opens the store,
// rebuilds state from the last 30 minutes of events, and serves until ctx ends.
func Run(ctx context.Context, o Options) error {
	if o.Logger == nil {
		o.Logger = log.New(io.Discard, "", 0)
	}
	ln, err := net.Listen("tcp", o.Addr)
	if err != nil {
		if isTrk(o.Addr) {
			return ErrAlreadyRunning
		}
		return fmt.Errorf("listen %s: %w", o.Addr, err)
	}
	if err := os.MkdirAll(o.DataDir, 0o755); err != nil {
		ln.Close()
		return err
	}
	st, err := store.Open(filepath.Join(o.DataDir, "trk.db"))
	if err != nil {
		ln.Close()
		return err
	}
	defer st.Close()

	fl := fleet.New(gitinfo.Lookup)
	if err := restore(st, fl, time.Now().UnixMilli()); err != nil {
		o.Logger.Printf("restore: %v", err) // degrade to empty state, keep serving
	}
	pidFile := filepath.Join(o.DataDir, "trk.pid")
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	defer os.Remove(pidFile)

	ctx, stopAll := context.WithCancel(ctx)
	defer stopAll()
	srv := NewServer(st, fl, o.Logger)
	defer srv.CloseTerminals()                        // dashboard-started agents end with the daemon
	if c := os.Getenv("TRK_AGENT_COMMAND"); c != "" { // testing: run something other than claude
		srv.terms.Command = c
	}
	if v, ok, err := st.Setting(settingTerminals); err == nil && ok {
		srv.SetTerminals(v == "true")
	}
	srv.OnShutdown = stopAll
	srv.OnStop = func() {
		if err := os.WriteFile(filepath.Join(o.DataDir, "paused"), []byte("paused from the dashboard\n"), 0o644); err != nil {
			o.Logger.Printf("pause: %v", err)
		}
		stopAll()
	}
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second} // no WriteTimeout: SSE
	loopCtx, stopLoop := context.WithCancel(ctx)
	defer stopLoop()
	go srv.Loop(loopCtx)
	if o.MaxDBBytes > 0 {
		go trimLoop(loopCtx, st, o.MaxDBBytes, o.Logger)
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if hs.Shutdown(sctx) != nil {
			hs.Close() // SSE connections never go idle
		}
	}()
	o.Logger.Printf("trk listening on http://%s", ln.Addr())
	if o.OnListen != nil {
		o.OnListen(ln.Addr().String())
	}
	if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func restore(st *store.Store, fl *fleet.Fleet, now int64) error {
	ss, err := st.Sessions()
	if err != nil {
		return err
	}
	acct, err := st.Account()
	if err != nil {
		return err
	}
	fl.Restore(ss, acct)
	if pids, err := st.PIDs(now - 24*3600*1000); err == nil { // Claude processes seen in the last day
		fl.RestorePIDs(pids)
	}
	fl.OnLearn = func(pid int, sid string) {
		_ = st.SavePID(pid, sid, time.Now().UnixMilli())
	}
	if v, ok, err := st.Setting("hide_after_min"); err == nil && ok {
		if m, err := strconv.Atoi(v); err == nil && m >= 0 {
			fl.SetHideAfter(int64(m) * 60000)
		}
	}
	evs, err := st.EventsSince(now - 30*60*1000)
	if err != nil {
		return err
	}
	for _, ev := range evs {
		fl.Apply(ev, now)
	}
	return nil
}

func isTrk(addr string) bool {
	c := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := c.Get("http://" + addr + "/healthz")
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return strings.Contains(string(b), `"app":"trk"`)
}

// trimLoop keeps the database under its size cap: once at startup, then hourly.
func trimLoop(ctx context.Context, st *store.Store, max int64, logger *log.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if n, err := st.TrimToSize(max); err != nil {
			logger.Printf("trim: %v", err)
		} else if n > 0 {
			logger.Printf("trim: removed %d oldest events to stay under %d MB", n, max>>20)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
