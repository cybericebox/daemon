package main

import (
	"github.com/cybericebox/daemon/internal/app"
	"github.com/cybericebox/daemon/internal/config"
)

func main() {
	cfg := config.MustGetConfig()
	config.SetupLogger(cfg)
	app.Run(cfg)
}
