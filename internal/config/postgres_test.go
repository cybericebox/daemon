package config_test

import (
	"testing"
	"time"

	"github.com/caarlos0/env/v11"

	"github.com/cybericebox/daemon/internal/config"
)

func TestPostgresPoolDefaultsAndValidation(t *testing.T) {
	var c config.PostgresConfig
	if err := env.ParseWithOptions(
		&c,
		env.Options{Prefix: "POSTGRES_", Environment: map[string]string{"POSTGRES_PASSWORD": "x"}},
	); err != nil {
		t.Fatal(err)
	}
	if c.MaxConns != 32 || c.AcquireTimeout != 3*time.Second ||
		c.MaxConnLifetime != 30*time.Minute ||
		c.MaxConnIdleTime != 5*time.Minute ||
		c.JobMaxConns != 8 {
		t.Fatalf("defaults: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	valid := c
	for name, mutate := range map[string]func(*config.PostgresConfig){
		"max conns below 4":         func(c *config.PostgresConfig) { c.MaxConns = 3 },
		"job max conns below 4":     func(c *config.PostgresConfig) { c.JobMaxConns = 3 },
		"acquire timeout too short": func(c *config.PostgresConfig) { c.AcquireTimeout = 99 * time.Millisecond },
		"acquire timeout too long":  func(c *config.PostgresConfig) { c.AcquireTimeout = 30*time.Second + 1 },
		"zero lifetime":             func(c *config.PostgresConfig) { c.MaxConnLifetime = 0 },
		"zero idle time":            func(c *config.PostgresConfig) { c.MaxConnIdleTime = 0 },
	} {
		bad := valid
		mutate(&bad)
		if bad.Validate() == nil {
			t.Errorf("%s must be refused", name)
		}
	}
	for _, edge := range []time.Duration{100 * time.Millisecond, 30 * time.Second} {
		ok := valid
		ok.AcquireTimeout = edge
		if err := ok.Validate(); err != nil {
			t.Errorf("acquire timeout %v must be accepted: %v", edge, err)
		}
	}
	vars := map[string]string{
		"POSTGRES_PASSWORD":           "x",
		"POSTGRES_MAX_CONNS":          "40",
		"POSTGRES_ACQUIRE_TIMEOUT":    "500ms",
		"POSTGRES_MAX_CONN_LIFETIME":  "1h",
		"POSTGRES_MAX_CONN_IDLE_TIME": "1m",
		"POSTGRES_JOB_MAX_CONNS":      "12",
	}
	if err := env.ParseWithOptions(
		&c,
		env.Options{Prefix: "POSTGRES_", Environment: vars},
	); err != nil {
		t.Fatal(err)
	}
	if c.MaxConns != 40 || c.AcquireTimeout != 500*time.Millisecond ||
		c.MaxConnLifetime != time.Hour ||
		c.MaxConnIdleTime != time.Minute ||
		c.JobMaxConns != 12 {
		t.Fatalf("env keys: %+v", c)
	}
}

func TestJobDefaultWorkersDefaultAndBounds(t *testing.T) {
	var c config.TunablesConfig
	if err := env.ParseWithOptions(&c, env.Options{Environment: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if c.JobDefaultWorkers != 6 {
		t.Fatalf("JOB_DEFAULT_WORKERS default = %d, want 6", c.JobDefaultWorkers)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 65} {
		bad := c
		bad.JobDefaultWorkers = n
		if bad.Validate() == nil {
			t.Errorf("JOB_DEFAULT_WORKERS=%d must be refused", n)
		}
	}
	for _, n := range []int{1, 64} {
		ok := c
		ok.JobDefaultWorkers = n
		if err := ok.Validate(); err != nil {
			t.Errorf("JOB_DEFAULT_WORKERS=%d must be accepted: %v", n, err)
		}
	}
}
