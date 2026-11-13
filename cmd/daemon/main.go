package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cybericebox/daemon/internal/app"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
)

const usage = "usage: daemon [admin-link | recompute-lab-telemetry <event id or tag> [--force] | erase-lab-telemetry <user id> | keys status [--strict] | keys check [<family> <key id>] | keys reencrypt <family> [--dry-run] [--yes] [--expect-key <id>]]"

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
	case (len(args) == 2 || (len(args) == 3 && args[2] == "--strict")) && args[0] == "keys" && args[1] == "status":
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		problems, err := app.RunKeysStatus(context.Background(), cfg, len(args) == 3, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "keys status:", err)
			os.Exit(2)
		}
		if problems > 0 {
			os.Exit(1)
		}
	case args[0] == "keys" && len(args) >= 2 && args[1] == "check" && (len(args) == 2 || len(args) == 4):
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		family, keyID := "", ""
		if len(args) == 4 {
			family, keyID = args[2], args[3]
		}
		code, err := app.RunKeysCheck(context.Background(), cfg, family, keyID, os.Stdout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "keys check:", err)
		}
		os.Exit(code)
	case len(args) >= 3 && args[0] == "keys" && args[1] == "reencrypt":
		dryRun, yes, ok, expectKey := false, false, true, ""
		for i := 3; i < len(args); i++ {
			switch args[i] {
			case "--dry-run":
				dryRun = true
			case "--yes":
				yes = true
			case "--expect-key":
				if i+1 >= len(args) {
					ok = false
					break
				}
				i++
				expectKey = args[i]
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
		rows, err := app.RunKeysReencrypt(context.Background(), cfg, args[2], dryRun, yes, expectKey, os.Stdout)
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
