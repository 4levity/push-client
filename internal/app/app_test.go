package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const cfgA = `
# test config
url=__URL__
name=sensor-1
token=secret0123456789abcdef0123456789abcdef
interval=1s
`

func writeCfg(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "conf")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func cfgFor(t *testing.T, url, interval string) string {
	t.Helper()
	return strings.Replace(strings.Replace(cfgA, "__URL__", url, 1), "interval=1s", "interval="+interval, 1)
}

func TestNewAndReload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := writeCfg(t, cfgFor(t, srv.URL, "1s"))
	a, err := New(path, discardLog())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if a.Config().Name != "sensor-1" || a.Config().Interval != time.Second {
		t.Errorf("unexpected config: %+v", a.Config())
	}

	// change the interval, reload, verify the atomic swap
	if err := os.WriteFile(path, []byte(cfgFor(t, srv.URL, "2s")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(path); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if a.Config().Interval != 2*time.Second {
		t.Errorf("Interval = %v, want 2s after reload", a.Config().Interval)
	}

	// invalid reload keeps the previous config
	if err := os.WriteFile(path, []byte("name=-bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(path); err == nil {
		t.Fatal("Reload: want error for invalid config")
	}
	if a.Config().Interval != 2*time.Second || a.Config().Name != "sensor-1" {
		t.Errorf("previous config should be kept after a failed reload: %+v", a.Config())
	}
}

func TestNewBadConfig(t *testing.T) {
	if _, err := New(writeCfg(t, "nope"), discardLog()); err == nil {
		t.Fatal("New: want error for invalid config")
	}
}

func TestNewWarnsOnLoosePerms(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "conf")
	if err := os.WriteFile(path, []byte(cfgFor(t, srv.URL, "1s")), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	if _, err := New(path, log); err != nil {
		t.Fatalf("New: %v", err)
	}
	if !strings.Contains(buf.String(), "group/world readable") {
		t.Errorf("want a permission warning, log: %s", buf.String())
	}
}

// TestRunPushesOnSchedule covers the whole loop: an immediate first push,
// then one per interval, surviving failed pushes, until cancellation. The
// minimum interval is 1s, so this test takes a couple of seconds.
func TestRunPushesOnSchedule(t *testing.T) {
	var ok, bad atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// The first push lands while the service is "down"; the next ticks
		// must go through (the next tick is the retry).
		if bad.Load() == 0 && ok.Load() == 0 {
			bad.Add(1)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":"down"}`)
			return
		}
		ok.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := writeCfg(t, cfgFor(t, srv.URL, "1s"))
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	a, err := New(path, log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()

	time.Sleep(2200 * time.Millisecond) // t=0 (fails), t=1s, t=2s
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := ok.Load(); got < 2 {
		t.Errorf("want at least 2 successful pushes, got %d", got)
	}
	if !strings.Contains(buf.String(), "status push failed") {
		t.Errorf("want a failure logged for the first push, log: %s", buf.String())
	}
}

func TestRunStopsImmediately(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	path := writeCfg(t, cfgFor(t, srv.URL, "1s"))
	a, err := New(path, discardLog())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	// The immediate first push happens before the first wait; cancelling
	// right away must stop the loop.
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after cancellation")
	}
}
