// Command push-client is a long-running client for the push status service:
// every interval it assembles this host's status payload and POSTs it to
// the service's public client API.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"pushclient/internal/app"
	"pushclient/internal/config"
	"syscall"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

// defaultConfigPath matches the path the ansible role installs the config
// to.
const defaultConfigPath = "/etc/push-client/conf"

const usageText = `push-client - long-running status sender for the push service (version %s)

Usage:
  push-client run       [-config /etc/push-client/conf] [-log-format json|text]
  push-client validate  [-config /etc/push-client/conf]

Commands:
  run       send a status every interval; SIGHUP reloads the config file
  validate  check the config file and exit
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, usageText, version)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]

	var err error
	switch cmd {
	case "run":
		err = run(args)
	case "validate":
		err = validate(args)
	case "help", "-h", "--help":
		fmt.Printf(usageText, version)
		return
	default:
		fmt.Fprintf(os.Stderr, "push-client: unknown command %q\n\n", cmd)
		fmt.Fprintf(os.Stderr, usageText, version)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to the config file")
	logFormat := fs.String("log-format", "json", "log format: json or text")
	_ = fs.Parse(args) //nolint:errcheck // ExitOnError: Parse exits on error

	log := newLogger(*logFormat)

	a, err := app.New(*cfgPath, log)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for {
			select {
			case <-hup:
				if err := a.Reload(*cfgPath); err != nil {
					log.Error("config reload failed; keeping previous config", "err", err)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	cfg := a.Config()
	log.Info("push-client starting",
		"version", version,
		"config", *cfgPath,
		"url", cfg.URL,
		"name", cfg.Name,
		"interval", cfg.Interval.String(),
		"status_files", len(cfg.StatusFiles))

	if err := a.Run(ctx); err != nil {
		return err
	}
	log.Info("push-client stopped")
	return nil
}

func validate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	cfgPath := fs.String("config", defaultConfigPath, "path to the config file")
	_ = fs.Parse(args) //nolint:errcheck // ExitOnError: Parse exits on error

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Printf("config OK: name=%s url=%s interval=%s %d status file(s)\n",
		cfg.Name, cfg.URL, cfg.Interval, len(cfg.StatusFiles))
	return nil
}

func newLogger(format string) *slog.Logger {
	var h slog.Handler
	switch format {
	case "text":
		h = slog.NewTextHandler(os.Stdout, nil)
	default:
		h = slog.NewJSONHandler(os.Stdout, nil)
	}
	return slog.New(h)
}
