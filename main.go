// Package main is the entry point for the gascity application.
// gascity is a fork of gastownhall/gascity, providing gas price
// monitoring and analysis tooling for EVM-compatible networks.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/gascity/gascity/internal/config"
	"github.com/gascity/gascity/internal/server"
)

var (
	// version is set at build time via ldflags.
	version = "dev"
	// commit is the git commit hash set at build time.
	commit = "none"
)

func main() {
	var (
		cfgPath  = flag.String("config", "config.yaml", "path to configuration file")
		printVer = flag.Bool("version", false, "print version information and exit")
		logLevel = flag.String("log-level", "info", "log level (debug, info, warn, error)")
	)
	flag.Parse()

	if *printVer {
		fmt.Printf("gascity %s (%s)\n", version, commit)
		os.Exit(0)
	}

	// Configure structured logging.
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		slog.Error("invalid log level", "level", *logLevel, "error", err)
		os.Exit(1)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
	}))
	slog.SetDefault(logger)

	slog.Info("starting gascity", "version", version, "commit", commit)

	// Load configuration.
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		slog.Error("failed to load configuration", "path", *cfgPath, "error", err)
		os.Exit(1)
	}

	// Set up context that cancels on OS signals.
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Initialise and run the server.
	srv, err := server.New(cfg, logger)
	if err != nil {
		slog.Error("failed to initialise server", "error", err)
		os.Exit(1)
	}

	if err := srv.Run(ctx); err != nil {
		slog.Error("server exited with error", "error", err)
		os.Exit(1)
	}

	slog.Info("gascity shut down cleanly")
}
