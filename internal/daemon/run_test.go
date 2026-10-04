package daemon

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func TestRunSingleInstanceAndPidFile(t *testing.T) {
	addr, dir := freeAddr(t), t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Addr: addr, DataDir: dir, Logger: log.New(io.Discard, "", 0), OnListen: func(string) { close(ready) }})
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not start")
	}
	if _, err := os.Stat(filepath.Join(dir, "trk.pid")); err != nil {
		t.Fatalf("pid file: %v", err)
	}
	err := Run(context.Background(), Options{Addr: addr, DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second Run = %v, want ErrAlreadyRunning", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown hung")
	}
	if _, err := os.Stat(filepath.Join(dir, "trk.pid")); !os.IsNotExist(err) {
		t.Fatal("pid file not removed")
	}
}

func TestRunPortTakenByStranger(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	defer l.Close()
	go http.Serve(l, http.NotFoundHandler())
	err := Run(context.Background(), Options{Addr: l.Addr().String(), DataDir: t.TempDir(), Logger: log.New(io.Discard, "", 0)})
	if err == nil || errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("err = %v, want a listen error", err)
	}
}
