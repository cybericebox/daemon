package ipam_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/cybericebox/daemon/pkg/ipam"
)

// TestIPAManager_AcquireRelease exercises the real go-ipam postgres storage:
// child CIDR acquisition, single-IP allocate/release, and first-IP math.
func TestIPAManager_AcquireRelease(t *testing.T) {
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx,
		"postgres:17-alpine",
		tcpostgres.WithDatabase("ipam"),
		tcpostgres.WithUsername("ipam"),
		tcpostgres.WithPassword("ipam"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("skipping integration test: cannot start postgres container (docker running?): %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("port: %v", err)
	}

	m, err := ipam.NewIPAManager(ipam.Dependencies{
		PostgresConfig: ipam.PostgresConfig{
			Host: host, Port: port.Port(),
			Username: "ipam", Password: "ipam", Database: "ipam", SSLMode: "disable",
		},
		CIDR: "10.128.0.0/16",
	})
	if err != nil {
		t.Fatalf("NewIPAManager: %v", err)
	}

	child, err := m.AcquireChildCIDR(ctx, 24)
	if err != nil {
		t.Fatalf("AcquireChildCIDR: %v", err)
	}
	first, err := child.GetFirstIP()
	if err != nil {
		t.Fatalf("GetFirstIP: %v", err)
	}
	childPrefix := netip.MustParsePrefix(child.GetCIDR())
	if !childPrefix.Contains(first) {
		t.Fatalf("first IP %s outside child CIDR %s", first, child.GetCIDR())
	}

	specific, err := child.AcquireSingleIP(ctx, first)
	if err != nil || specific != first {
		t.Fatalf("AcquireSingleIP(specific): %v %v", specific, err)
	}
	next, err := child.AcquireSingleIP(ctx)
	if err != nil {
		t.Fatalf("AcquireSingleIP: %v", err)
	}
	if next == first {
		t.Fatal("second allocation must differ from the first")
	}
	if err = child.ReleaseSingleIP(ctx, next); err != nil {
		t.Fatalf("ReleaseSingleIP: %v", err)
	}
	if err = m.ReleaseChildCIDR(ctx, child.GetCIDR()); err != nil {
		t.Fatalf("ReleaseChildCIDR: %v", err)
	}
}
