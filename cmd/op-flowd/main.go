package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/op-flow-insight/op-flow-insight/internal/collector"
	"github.com/op-flow-insight/op-flow-insight/internal/config"
	"github.com/op-flow-insight/op-flow-insight/internal/conntrack"
	"github.com/op-flow-insight/op-flow-insight/internal/dataset"
	"github.com/op-flow-insight/op-flow-insight/internal/server"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Printf("op-flowd: %v", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("op-flowd", flag.ContinueOnError)
	configPath := flags.String("config", "/etc/config/op-flow", "UCI configuration path")
	showVersion := flags.Bool("version", false, "print version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	command := "daemon"
	rest := flags.Args()
	if len(rest) > 0 {
		command = rest[0]
		rest = rest[1:]
	}
	data := dataset.NewManager(cfg.DataDir)
	_ = data.Reload()
	updates := server.NewCoordinator(cfg.DataDir, data)

	switch command {
	case "daemon":
		if !cfg.Enabled {
			return nil
		}
		return runDaemon(cfg, data, updates)
	case "ctl":
		if len(rest) == 0 {
			return fmt.Errorf("ctl requires dashboard, health, lookup, history, export, update, or reset")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		return server.Ctl(ctx, cfg.SocketPath, rest[0], rest[1:], os.Stdout)
	case "update-data":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		if err := updates.UpdateSync(ctx); err != nil {
			return err
		}
		raw, _ := json.MarshalIndent(data.Status(), "", "  ")
		fmt.Println(string(raw))
		return nil
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func runDaemon(cfg config.Config, data *dataset.Manager, updates *server.Coordinator) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	tracker, err := collector.New(cfg, data, version)
	if err != nil {
		return err
	}
	api := server.NewAPI(tracker, data, updates)
	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.ServeUnix(ctx, cfg.SocketPath, api.Handler())
	}()
	go tracker.Run(ctx)
	go func() {
		tracker.SetDestroyEventHealth(true, nil)
		if err := conntrack.ListenDestroy(ctx, tracker.ApplyDestroy); err != nil && ctx.Err() == nil {
			tracker.SetDestroyEventHealth(false, err)
		}
	}()
	if cfg.AutoUpdate {
		go autoUpdate(ctx, cfg, data, updates)
	}
	select {
	case err := <-serverErrors:
		stop()
		return err
	case <-ctx.Done():
		return tracker.Save()
	}
}

func autoUpdate(ctx context.Context, cfg config.Config, data *dataset.Manager, updates *server.Coordinator) {
	check := func() {
		status := data.Status()
		if status.LastUpdateError != "" || status.UpdatedAt.IsZero() || time.Since(status.UpdatedAt) >= cfg.UpdateInterval {
			updates.Trigger(ctx)
		}
	}
	// Let networking settle after boot.
	timer := time.NewTimer(45 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
		check()
	case <-ctx.Done():
		return
	}
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			check()
		case <-ctx.Done():
			return
		}
	}
}
