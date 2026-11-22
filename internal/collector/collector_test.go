package collector

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"pushclient/internal/config"
	"strings"
	"testing"
)

func testLogger(t *testing.T) (*slog.Logger, *strings.Builder) {
	t.Helper()
	var buf strings.Builder
	log := slog.New(slog.NewTextHandler(&buf, nil))
	return log, &buf
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// canonical renders v as compact JSON (map keys sorted) for comparison.
func canonical(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func mustAny(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func samePayload(t *testing.T, got map[string]any, wantJSON string) {
	t.Helper()
	if have, want := canonical(t, got), canonical(t, mustAny(t, wantJSON)); have != want {
		t.Fatalf("payload = %s, want %s", have, want)
	}
}

func TestCollectMergesBaseAndExtras(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.json", `{"a":1,"n":{"x":1},"keep":true}`)
	ex1 := writeFile(t, dir, "ex1.json", `{"b":2,"n":{"y":2}}`)
	ex2 := writeFile(t, dir, "ex2.json", `{"b":3}`)
	log, _ := testLogger(t)

	cfg := &config.Config{Status: base, StatusFiles: []string{ex1, ex2}}
	got := Collect(cfg, log)
	// Shallow merge: the nested "n" object is replaced wholesale; later
	// files win.
	samePayload(t, got, `{"a":1,"b":3,"n":{"y":2},"keep":true}`)
}

func TestCollectNoBase(t *testing.T) {
	dir := t.TempDir()
	ex1 := writeFile(t, dir, "ex1.json", `{"b":2}`)
	log, buf := testLogger(t)

	cfg := &config.Config{Status: filepath.Join(dir, "missing.json"), StatusFiles: []string{ex1}}
	got := Collect(cfg, log)
	samePayload(t, got, `{"b":2}`)
	if !strings.Contains(buf.String(), "base status unreadable") {
		t.Errorf("want a warning for the missing base, log: %s", buf.String())
	}
}

func TestCollectBrokenBase(t *testing.T) {
	dir := t.TempDir()
	base := writeFile(t, dir, "base.json", `[1,2]`)
	ex1 := writeFile(t, dir, "ex1.json", `{"b":2}`)
	log, _ := testLogger(t)

	cfg := &config.Config{Status: base, StatusFiles: []string{ex1}}
	samePayload(t, Collect(cfg, log), `{"b":2}`)
}

func TestCollectExtraMissingNoCache(t *testing.T) {
	dir := t.TempDir()
	ex1 := writeFile(t, dir, "ex1.json", `{"a":1}`)
	log, buf := testLogger(t)

	cfg := &config.Config{StatusFiles: []string{ex1, filepath.Join(dir, "missing.json")}}
	samePayload(t, Collect(cfg, log), `{"a":1}`)
	if !strings.Contains(buf.String(), "skipped") {
		t.Errorf("want a warning for the skipped file, log: %s", buf.String())
	}
}

func TestCollectExtraFallsBackToCache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ex1 := writeFile(t, dir, "ex1.json", `{"a":1}`)
	// Seed the cache with a last good copy of the missing file.
	missing := filepath.Join(dir, "missing.json")
	writeFile(t, cacheDir, filepath.Base(CachePath(cacheDir, missing)), `{"m":9}`)
	log, buf := testLogger(t)

	cfg := &config.Config{CacheDir: cacheDir, StatusFiles: []string{ex1, missing}}
	samePayload(t, Collect(cfg, log), `{"a":1,"m":9}`)
	if !strings.Contains(buf.String(), "last good copy") {
		t.Errorf("want a warning about the cache fallback, log: %s", buf.String())
	}
}

func TestCollectBrokenExtraFallsBackToCache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ex1 := writeFile(t, dir, "ex1.json", `not json`)
	writeFile(t, cacheDir, filepath.Base(CachePath(cacheDir, ex1)), `{"c":1}`)
	log, _ := testLogger(t)

	cfg := &config.Config{CacheDir: cacheDir, StatusFiles: []string{ex1}}
	samePayload(t, Collect(cfg, log), `{"c":1}`)
}

func TestCollectBrokenExtraAndBrokenCache(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	ex1 := writeFile(t, dir, "ex1.json", `not json`)
	writeFile(t, cacheDir, filepath.Base(CachePath(cacheDir, ex1)), `also not json`)
	log, buf := testLogger(t)

	cfg := &config.Config{CacheDir: cacheDir, StatusFiles: []string{ex1}}
	samePayload(t, Collect(cfg, log), `{}`)
	if !strings.Contains(buf.String(), "skipped") {
		t.Errorf("want a warning for the skipped file, log: %s", buf.String())
	}
}

func TestCollectUpdatesCacheAndSurvivesLoss(t *testing.T) {
	dir := t.TempDir()
	cacheDir := filepath.Join(dir, "cache")
	ex1 := writeFile(t, dir, "ex1.json", `{"a":1}`)
	log, _ := testLogger(t)
	cfg := &config.Config{CacheDir: cacheDir, StatusFiles: []string{ex1}}

	samePayload(t, Collect(cfg, log), `{"a":1}`)

	// The live file is lost; the next collect must still carry its value.
	if err := os.Remove(ex1); err != nil {
		t.Fatal(err)
	}
	samePayload(t, Collect(cfg, log), `{"a":1}`)
}

func TestCollectBigIntegersSurvive(t *testing.T) {
	dir := t.TempDir()
	ex1 := writeFile(t, dir, "ex1.json", `{"ns":1721234567890123456}`)
	log, _ := testLogger(t)

	cfg := &config.Config{StatusFiles: []string{ex1}}
	got := Collect(cfg, log)
	if have := canonical(t, got); !strings.Contains(have, "1721234567890123456") {
		t.Errorf("large integer mangled: %s", have)
	}
}

func TestCollectExtraNotObject(t *testing.T) {
	dir := t.TempDir()
	ex1 := writeFile(t, dir, "ex1.json", `[1,2]`)
	log, buf := testLogger(t)

	cfg := &config.Config{StatusFiles: []string{ex1}}
	samePayload(t, Collect(cfg, log), `{}`)
	if !strings.Contains(buf.String(), "skipped") {
		t.Errorf("want a warning for the non-object file, log: %s", buf.String())
	}
}

func TestCachePathNaming(t *testing.T) {
	// The same layout the bash reference client used.
	got := CachePath("/var/lib/push-client", "/opt/backup/status.json")
	const want = "/var/lib/push-client/_opt_backup_status_json"
	if got != want {
		t.Errorf("CachePath = %q, want %q", got, want)
	}
}
