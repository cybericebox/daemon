package app

import (
	"github.com/cybericebox/daemon/internal/config"
	"log"
)

func Run() {
	// Start the application
	cfg, valid := config.GetConfig()
	if !valid {
		log.Fatal("Configuration is not valid. Exiting...")
	}
	println(cfg.Environment)
}
