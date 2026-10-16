package postgres

import "testing"

// The pool must outnumber the River workers (12), or jobs holding a transaction starve requests and the session poll.
func TestNewPoolConfigSizesThePoolAboveTheWorkers(t *testing.T) {
	cfg, err := newPoolConfig("postgres://u:p@localhost:5432/db?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxConns != 32 {
		t.Fatalf("MaxConns = %d, want 32", cfg.MaxConns)
	}
}
