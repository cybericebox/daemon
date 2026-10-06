package main

import "testing"

func TestViolations(t *testing.T) {
	src := `
import (
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
)`
	got := violations(src)
	if len(got) != 2 {
		t.Fatalf("want 2 violations (delivery + useCase), got %d: %v", len(got), got)
	}
}
