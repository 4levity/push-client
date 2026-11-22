// Package app runs the push client: the push schedule, the status
// collection and the config reload.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"pushclient/internal/collector"
	"pushclient/internal/config"
	"pushclient/internal/push"
	"sync/atomic"
	"time"
)

// App owns the live configuration and the sender.
type App struct {
	cfg    atomic.Pointer[config.Config]
	sender *push.Sender
	log    *slog.Logger
}

// New loads the config file at path and builds a ready-to-run App.
func New(path string, log *slog.Logger) (*App, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	a := &App{sender: push.New(), log: log}
	a.cfg.Store(cfg)
	if w := config.PermsWarning(path); w != "" {
		a.log.Warn(w)
	}
	return a, nil
}

// Config returns the active configuration.
func (a *App) Config() *config.Config { return a.cfg.Load() }

// Reload re-reads the config file and atomically swaps it in on success.
// If the new file is invalid the previous configuration keeps running.
// The next tick uses the new values, including the interval.
func (a *App) Reload(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("config %s is invalid: %w", path, err)
	}
	a.cfg.Store(cfg)
	a.log.Info("config reloaded", "name", cfg.Name, "interval", cfg.Interval.String())
	if w := config.PermsWarning(path); w != "" {
		a.log.Warn(w)
	}
	return nil
}

// Run pushes a status immediately, then every configured interval, until
// ctx is cancelled. A failing push is logged and the schedule continues:
// the next tick is the retry.
func (a *App) Run(ctx context.Context) error {
	for {
		cfg := a.cfg.Load()
		a.pushOnce(ctx, cfg)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(cfg.Interval):
		}
	}
}

func (a *App) pushOnce(ctx context.Context, cfg *config.Config) {
	payload := collector.Collect(cfg, a.log)
	data, err := marshalPayload(payload)
	if err != nil {
		a.log.Error("marshaling the status failed", "err", err)
		return
	}
	if err := a.sender.Send(ctx, cfg.URL, cfg.Name, cfg.Token, data); err != nil {
		// At shutdown the in-flight push is cancelled; that is not a push
		// failure worth logging.
		if !errors.Is(err, context.Canceled) {
			a.log.Error("status push failed", "name", cfg.Name, "url", cfg.URL, "err", err)
		}
		return
	}
	a.log.Info("status pushed", "name", cfg.Name, "bytes", len(data))
}

// marshalPayload renders the payload compactly, without HTML escaping, so
// the wire bytes stay close to the status files' own content.
func marshalPayload(payload map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
