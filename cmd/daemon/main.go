package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cybericebox/daemon/internal/app"
	"github.com/cybericebox/daemon/internal/config"
)

const usage = "usage: daemon [admin-link]"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		cfg := config.MustGetConfig()
		config.SetupLogger(cfg)
		app.Run(cfg)
		return
	}
	if len(args) != 1 || args[0] != "admin-link" {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	cfg := config.MustGetConfig()
	config.SetupLogger(cfg)
	if err := app.RunAdminLink(context.Background(), cfg, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "admin-link:", err)
		os.Exit(1)
	}
}
