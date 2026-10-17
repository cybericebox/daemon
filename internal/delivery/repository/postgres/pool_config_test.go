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

func TestPoolBudgetStatesTheArithmetic(t *testing.T) {
	want := "DB pools: main=32 job=8 per_replica=40 (with N replicas need N×40+20 ≤ max_connections)"
	if got := poolBudget(32, 8); got != want {
		t.Fatalf("poolBudget = %q, want %q", got, want)
	}
}

// One replica plus the reserved room must fit the server: 32+8+20 = 60.
func TestPoolBudgetExceeded(t *testing.T) {
	cases := map[int]bool{200: false, 60: false, 59: true, 40: true}
	for maxConnections, want := range cases {
		if got := poolBudgetExceeded(32, 8, maxConnections); got != want {
			t.Errorf("max_connections=%d: exceeded = %t, want %t", maxConnections, got, want)
		}
	}
}
