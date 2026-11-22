package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const valid = `
# a comment
url=https://push.example.com
name=sensor-1
token=secret0123456789abcdef0123456789abcdef
interval=5m
status=/etc/push-client/status.json
status_file=/opt/backup/status.json
status_file=/opt/other/status.json
cache=/var/lib/push-client
`

func TestParseValid(t *testing.T) {
	cfg, err := Parse([]byte(valid))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.URL != "https://push.example.com" {
		t.Errorf("URL = %q", cfg.URL)
	}
	if cfg.Name != "sensor-1" {
		t.Errorf("Name = %q", cfg.Name)
	}
	if cfg.Token != "secret0123456789abcdef0123456789abcdef" {
		t.Errorf("Token = %q", cfg.Token)
	}
	if cfg.Interval != 5*time.Minute {
		t.Errorf("Interval = %v", cfg.Interval)
	}
	if cfg.Status != "/etc/push-client/status.json" {
		t.Errorf("Status = %q", cfg.Status)
	}
	if len(cfg.StatusFiles) != 2 ||
		cfg.StatusFiles[0] != "/opt/backup/status.json" ||
		cfg.StatusFiles[1] != "/opt/other/status.json" {
		t.Errorf("StatusFiles = %v", cfg.StatusFiles)
	}
	if cfg.CacheDir != "/var/lib/push-client" {
		t.Errorf("CacheDir = %q", cfg.CacheDir)
	}
}

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse([]byte("url=https://h\nname=a\ntoken=t\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Interval != DefaultInterval {
		t.Errorf("Interval = %v, want the default %v", cfg.Interval, DefaultInterval)
	}
	if cfg.Status != "" || len(cfg.StatusFiles) != 0 || cfg.CacheDir != "" {
		t.Errorf("optional paths should be empty: %+v", cfg)
	}
}

func TestParseTrailingSlashStripped(t *testing.T) {
	cfg, err := Parse([]byte("url=https://h:8080/push/\nname=a\ntoken=t\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.URL != "https://h:8080/push" {
		t.Errorf("URL = %q", cfg.URL)
	}
}

func TestParseCRLF(t *testing.T) {
	if _, err := Parse([]byte("url=https://h\r\nname=a\r\ntoken=t\r\n")); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestParseEmptyOptionalKeyIsUnset(t *testing.T) {
	cfg, err := Parse([]byte("url=https://h\nname=a\ntoken=t\ninterval=\ncache=\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Interval != DefaultInterval || cfg.CacheDir != "" {
		t.Errorf("empty optional keys should be unset: %+v", cfg)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"missing all", "", "url is required"},
		{"missing url", "name=a\ntoken=t\n", "url is required"},
		{"missing name", "url=https://h\ntoken=t\n", "name is required"},
		{"missing token", "url=https://h\nname=a\n", "token is required"},
		{"bad name leading dash", "url=https://h\nname=-a\ntoken=t\n", "letters, numbers and dashes"},
		{"bad name double dash", "url=https://h\nname=a--b\ntoken=t\n", "letters, numbers and dashes"},
		{"bad name trailing dash", "url=https://h\nname=a-\ntoken=t\n", "letters, numbers and dashes"},
		{"no scheme", "url=push.example.com\nname=a\ntoken=t\n", "scheme"},
		{"ftp scheme", "url=ftp://h\nname=a\ntoken=t\n", "scheme"},
		{"no host", "url=https://\nname=a\ntoken=t\n", "host"},
		{"url user info", "url=https://u@h\nname=a\ntoken=t\n", "user info"},
		{"bad interval", "url=https://h\nname=a\ntoken=t\ninterval=soon\n", "invalid duration"},
		{"tiny interval", "url=https://h\nname=a\ntoken=t\ninterval=100ms\n", "at least"},
		{"negative interval", "url=https://h\nname=a\ntoken=t\ninterval=-5m\n", "at least"},
		{"relative status", "url=https://h\nname=a\ntoken=t\nstatus=relative.json\n", "absolute path"},
		{"relative status_file", "url=https://h\nname=a\ntoken=t\nstatus_file=extra.json\n", "absolute path"},
		{"relative cache", "url=https://h\nname=a\ntoken=t\ncache=cache\n", "absolute path"},
		{"unknown key", "url=https://h\nname=a\ntoken=t\nbogus=1\n", "unknown key"},
		{"duplicate key", "url=https://h\nname=a\nname=b\ntoken=t\n", "duplicate"},
		{"not a key value line", "url=https://h\nname=a\ntoken=t\njust some text\n", "not a key=value"},
		{"empty status_file", "url=https://h\nname=a\ntoken=t\nstatus_file=\n", "needs a path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.content))
			if err == nil {
				t.Fatalf("Parse: want error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Parse error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestParseMultipleErrorsReported(t *testing.T) {
	_, err := Parse([]byte("name=-x\n"))
	if err == nil {
		t.Fatal("Parse: want error")
	}
	for _, want := range []string{"url is required", "token is required", "letters, numbers and dashes"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conf")
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Load: want error for missing file")
	}
}
