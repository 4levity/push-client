// Package config loads and validates the push client config file.
//
// The file is line-based: one key=value per line, with # comments and
// blank lines allowed. status_file may repeat (the merge order is the
// file order); every other key may appear at most once.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultInterval applies when the config has no interval line: 5m, well
// inside the service's per-IP rate limit (60 requests/minute).
const DefaultInterval = 5 * time.Minute

// MinInterval is the smallest accepted interval. Pushing more often than
// once a second would run into the service's default rate limit.
const MinInterval = time.Second

// The config file keys.
const (
	KeyURL        = "url"
	KeyName       = "name"
	KeyToken      = "token"
	KeyInterval   = "interval"
	KeyStatus     = "status"
	KeyStatusFile = "status_file"
	KeyCache      = "cache"
)

// nameRe mirrors the service's client-name rule: one or more alphanumerics,
// optionally followed by dash+alphanumerics.
var nameRe = regexp.MustCompile(`^[A-Za-z0-9]+(-?[A-Za-z0-9]+)*$`)

// Config is a validated push client configuration.
type Config struct {
	URL         string // normalized: no trailing slash
	Name        string
	Token       string
	Interval    time.Duration
	Status      string   // base status file ("" when unset)
	StatusFiles []string // extra status files, merged in order (later wins)
	CacheDir    string   // last-good-copy directory for StatusFiles ("" when unset)
}

// Load reads and validates the config file at path.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return Parse(data)
}

// Parse decodes and validates raw config file bytes.
func Parse(data []byte) (*Config, error) {
	values := map[string]string{}
	seen := map[string]int{}
	var statusFiles []string
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw) // also drops a trailing \r
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			add("line %d: %q is not a key=value line", i+1, line)
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case KeyStatusFile:
			if value == "" {
				add("line %d: status_file= needs a path", i+1)
			} else {
				statusFiles = append(statusFiles, value)
			}
		case KeyURL, KeyName, KeyToken, KeyInterval, KeyStatus, KeyCache:
			if prev, dup := seen[key]; dup {
				add("line %d: duplicate %s= (first set on line %d)", i+1, key, prev)
				continue
			}
			// An empty value on an optional key is treated as unset; the
			// required keys are reported missing below.
			if value == "" && key != KeyURL && key != KeyName && key != KeyToken {
				continue
			}
			seen[key] = i + 1
			values[key] = value
		default:
			add("line %d: unknown key %q", i+1, key)
		}
	}

	cfg := &Config{
		URL:         values[KeyURL],
		Name:        values[KeyName],
		Token:       values[KeyToken],
		Interval:    DefaultInterval,
		Status:      values[KeyStatus],
		StatusFiles: statusFiles,
		CacheDir:    values[KeyCache],
	}

	if cfg.URL == "" {
		add("url is required (the push service endpoint, e.g. https://push.example.com)")
	} else if u, err := normalizeURL(cfg.URL); err != nil {
		add("url %q: %v", cfg.URL, err)
	} else {
		cfg.URL = u
	}
	if cfg.Name == "" {
		add("name is required")
	} else if !nameRe.MatchString(cfg.Name) {
		add("name %q: must contain only letters, numbers and dashes", cfg.Name)
	}
	if cfg.Token == "" {
		add("token is required (the raw client token)")
	}
	if iv := values[KeyInterval]; iv != "" {
		d, err := time.ParseDuration(iv)
		switch {
		case err != nil:
			add("interval %q: invalid duration (examples: 30s, 5m, 1h)", iv)
		case d < MinInterval:
			add("interval %q: must be at least %s (the service rate-limits to 60 requests/minute per IP)", iv, MinInterval)
		default:
			cfg.Interval = d
		}
	}
	if cfg.Status != "" && !filepath.IsAbs(cfg.Status) {
		add("status %q: must be an absolute path", cfg.Status)
	}
	for i, f := range cfg.StatusFiles {
		if !filepath.IsAbs(f) {
			add("status_file[%d] %q: must be an absolute path", i, f)
		}
	}
	if cfg.CacheDir != "" && !filepath.IsAbs(cfg.CacheDir) {
		add("cache %q: must be an absolute path", cfg.CacheDir)
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

// normalizeURL validates the endpoint and strips a trailing slash so the
// client can build url+"/"+name.
func normalizeURL(s string) (string, error) {
	u, err := url.Parse(s)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("the scheme must be http or https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	if u.User != nil {
		return "", fmt.Errorf("user info is not allowed")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("query and fragment are not allowed")
	}
	return strings.TrimSuffix(u.String(), "/"), nil
}

// PermsWarning reports a concern about the config file's permissions: the
// file holds the raw client token, so group/world-readable modes are
// flagged. It is a warning, not an error: the operator may have a reason.
func PermsWarning(path string) string {
	fi, err := os.Stat(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return ""
	}
	if fi.Mode().Perm()&0o044 != 0 {
		return fmt.Sprintf("config file %s is group/world readable (mode %o) and holds the raw client token", path, fi.Mode().Perm())
	}
	return ""
}
