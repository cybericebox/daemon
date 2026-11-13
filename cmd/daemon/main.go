package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cybericebox/daemon/internal/app"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
)

const usage = "usage: daemon [admin-link | recompute-lab-telemetry <event id or tag> [--force] | erase-lab-telemetry <user id> | keys status | keys reencrypt <family> [--dry-run] [--yes]]"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		app.Run(cfg)
		return
	}
	switch {
	case len(args) == 1 && args[0] == "admin-link":
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		if err := app.RunAdminLink(context.Background(), cfg, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "admin-link:", err)
			os.Exit(1)
		}
	case (len(args) == 2 || (len(args) == 3 && args[2] == "--force")) && args[0] == "recompute-lab-telemetry":
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		if err := app.RunRecomputeLabTelemetry(context.Background(), cfg, args[1], len(args) == 3, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "recompute-lab-telemetry:", err)
			os.Exit(1)
		}
	case len(args) == 2 && args[0] == "erase-lab-telemetry":
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		if err := app.RunEraseLabTelemetry(context.Background(), cfg, args[1], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "erase-lab-telemetry:", err)
			os.Exit(1)
		}
	case len(args) == 2 && args[0] == "keys" && args[1] == "status":
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		problems, err := app.RunKeysStatus(context.Background(), cfg, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "keys status:", err)
			os.Exit(2)
		}
		if problems > 0 {
			os.Exit(1)
		}
	case len(args) >= 3 && args[0] == "keys" && args[1] == "reencrypt":
		dryRun, yes, ok := false, false, true
		for _, a := range args[3:] {
			switch a {
			case "--dry-run":
				dryRun = true
			case "--yes":
				yes = true
			default:
				ok = false
			}
		}
		if !ok || args[2] == "--help" {
			fmt.Fprintln(os.Stderr, keyrotation.Help)
			os.Exit(2)
		}
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		rows, err := app.RunKeysReencrypt(context.Background(), cfg, args[2], dryRun, yes, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "keys reencrypt:", err)
			os.Exit(2)
		}
		if rows > 0 {
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}
