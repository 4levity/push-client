// Package collector assembles the status payload sent to the push service:
// the base status file with the extra status files merged over it (shallow
// merge, later files win). Every extra has a last-good copy cached on this
// host, so a missing or broken file never stops the heartbeat.
package collector

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"pushclient/internal/config"
	"regexp"
)

// cacheKeyRe mirrors the reference client's cache naming: every run of
// non-alphanumerics in the file path becomes one underscore, so the cache
// layout carries over from the bash client.
var cacheKeyRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// CachePath returns the path of the last-good copy for file in dir.
func CachePath(dir, file string) string {
	return filepath.Join(dir, cacheKeyRe.ReplaceAllString(file, "_"))
}

// Collect assembles the payload. It never fails: a missing or broken base
// becomes {}, a missing or broken extra falls back to its cached last good
// copy (when one exists) or is skipped, and every miss is logged. The
// heartbeat goes out either way.
func Collect(cfg *config.Config, log *slog.Logger) map[string]any {
	payload := map[string]any{}
	if cfg.Status != "" {
		obj, err := loadObject(cfg.Status)
		if err != nil {
			log.Warn("base status unreadable; sending an empty base", "file", cfg.Status, "err", err)
		} else {
			payload = obj
		}
	}
	for _, f := range cfg.StatusFiles {
		obj, err := loadObject(f)
		if err == nil {
			merge(payload, obj)
			if cfg.CacheDir != "" {
				if cerr := cachePut(cfg.CacheDir, f, obj); cerr != nil {
					log.Error("last good copy could not be cached", "file", f, "err", cerr)
				}
			}
			continue
		}
		why := err
		if cfg.CacheDir != "" {
			if cached, cerr := loadObject(CachePath(cfg.CacheDir, f)); cerr == nil {
				merge(payload, cached)
				log.Warn("status file unreadable; using the last good copy from the cache", "file", f, "err", why)
				continue
			}
		}
		log.Warn("status file unreadable; skipped", "file", f, "err", why)
	}
	return payload
}

// loadObject reads path and requires it to hold a single JSON object
// document. Numbers keep their exact textual form (json.Number).
func loadObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, err
	}
	return ParseObject(data)
}

// ParseObject requires data to be a single JSON object document; anything
// else (a JSON array, a second document, trailing garbage) is an error.
// Numbers are kept as json.Number so large integers survive the
// round trip exactly.
func ParseObject(data []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	// A second Decode must hit EOF: a second document or trailing garbage
	// would otherwise be silently ignored.
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("more than one JSON document")
		}
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("not a JSON object")
	}
	return obj, nil
}

// merge overlays src onto dst: top-level keys, later wins (shallow).
func merge(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = v
	}
}

// cachePut atomically stores obj as the last good copy of file in dir.
func cachePut(dir, file string, obj map[string]any) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	p := CachePath(dir, file)
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		_ = os.Remove(tmp) //nolint:errcheck // best-effort cleanup; the rename error is what we return
		return err
	}
	return nil
}
