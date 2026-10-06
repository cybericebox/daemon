package config

import (
	"os"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// SetupLogger switches gin and zerolog by ENV (Config.Environment). Only local
// development gets human-readable output: gin debug mode (route dump, gin's
// coloured request lines) and console app logs. Every other environment
// (stage, production, anything unknown) runs gin in release mode and logs
// everything, requests included, as zerolog JSON. The level stays debug
// outside production, as in the old daemon.
// Call it before building the router: middleware.ForMode reads gin's mode.
func SetupLogger(cfg *Config) {
	gin.SetMode(GinMode(cfg.Environment))
	zerolog.SetGlobalLevel(LogLevel(cfg.Environment))
	if ConsoleLogs(cfg.Environment) {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr})
	}
}

// ConsoleLogs reports whether logs are human-readable (development) rather
// than JSON (everything else).
func ConsoleLogs(environment string) bool {
	return environment == Development
}

// GinMode is gin's mode for an environment: debug only in development, so the
// route dump and gin's text request log never reach a JSON environment.
func GinMode(environment string) string {
	if ConsoleLogs(environment) {
		return gin.DebugMode
	}
	return gin.ReleaseMode
}

// LogLevel is zerolog's global level for an environment: debug outside
// production.
func LogLevel(environment string) zerolog.Level {
	if environment == Production {
		return zerolog.InfoLevel
	}
	return zerolog.DebugLevel
}
