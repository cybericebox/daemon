package postgres

import (
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/config"
)

// Each pool takes its own size and the shared connection lifetime and idle time from the configuration.
func TestNewPoolConfigTakesSizeAndLifetimes(t *testing.T) {
	cfg := &config.PostgresConfig{
		MaxConnLifetime: 30 * time.Minute,
		MaxConnIdleTime: 5 * time.Minute,
	}
	for _, size := range []int{32, 8} {
		pc, err := newPoolConfig("postgres://u:p@localhost:5432/db?sslmode=disable", size, cfg)
		if err != nil {
			t.Fatal(err)
		}
		if int(pc.MaxConns) != size || pc.MaxConnLifetime != 30*time.Minute ||
			pc.MaxConnIdleTime != 5*time.Minute {
			t.Fatalf(
				"size %d: MaxConns=%d lifetime=%v idle=%v",
				size,
				pc.MaxConns,
				pc.MaxConnLifetime,
				pc.MaxConnIdleTime,
			)
		}
	}
}
