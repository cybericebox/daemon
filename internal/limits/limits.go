// Package limits holds the process-wide abuse limits (config.LimitsConfig),
// set once at start. Until Set is called (tests) the env defaults apply.
package limits

import (
	"sync/atomic"

	"github.com/caarlos0/env/v11"

	"github.com/cybericebox/daemon/internal/config"
)

var current atomic.Pointer[config.LimitsConfig]

func defaults() config.LimitsConfig {
	var c config.LimitsConfig
	if err := env.ParseWithOptions(&c, env.Options{Prefix: "LIMIT_", Environment: map[string]string{}}); err != nil {
		panic("limits: defaults do not parse: " + err.Error())
	}
	return c
}

// Set installs the configured limits.
func Set(c config.LimitsConfig) { current.Store(&c) }

// Get returns the configured limits, or the defaults before Set.
func Get() config.LimitsConfig {
	if c := current.Load(); c != nil {
		return *c
	}
	return defaults()
}
