package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cybericebox/daemon/internal/app"
	"github.com/cybericebox/daemon/internal/config"
)

const usage = "usage: daemon [admin-link | recompute-lab-telemetry <event id or tag> | erase-lab-telemetry <user id>]"

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
	case len(args) == 2 && args[0] == "recompute-lab-telemetry":
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		if err := app.RunRecomputeLabTelemetry(context.Background(), cfg, args[1], os.Stdout); err != nil {
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
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}
