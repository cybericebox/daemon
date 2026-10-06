package labagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

func TestWindowsOfConvertsTheAgentsReport(t *testing.T) {
	from := time.Date(2026, 10, 12, 22, 0, 0, 0, time.UTC)
	got := WindowsOf(&labpb.MaintenanceWindowList{Items: []*labpb.MaintenanceWindow{
		{Name: "kernel", Reason: "upgrade", FromUnixMs: from.UnixMilli(), ToUnixMs: from.Add(4 * time.Hour).UnixMilli(), State: "Upcoming", AllTenants: true},
		{Name: "open", FromUnixMs: from.UnixMilli(), HasCapacity: true, CapacityCpuMillicores: 500, CapacityMemoryBytes: 1 << 30},
	}})
	if len(got) != 2 {
		t.Fatalf("windows = %+v", got)
	}
	k, o := got[0], got[1]
	if k.Name != "kernel" || !k.From.Equal(from) || k.To == nil || !k.To.Equal(from.Add(4*time.Hour)) || !k.AllTenants || k.HasCapacity {
		t.Errorf("kernel = %+v", k)
	}
	if o.To != nil || !o.HasCapacity || o.CPUMillicores != 500 || o.MemoryBytes != 1<<30 {
		t.Errorf("open ended, leaving some capacity = %+v", o)
	}
	if got := WindowsOf(nil); got == nil || len(got) != 0 {
		t.Errorf("no report is an empty list, never nil: %v", got)
	}
}

// A failed read records nothing; a good one is recorded; the poll ends with its context.
func TestPollMaintenanceRecordsOnlySuccessfulReads(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var mu sync.Mutex
	calls, recorded := 0, 0
	done := make(chan struct{})
	go func() {
		defer close(done)
		PollMaintenance(ctx, "eu", time.Millisecond, func(context.Context) (*labpb.MaintenanceWindowList, error) {
			mu.Lock()
			defer mu.Unlock()
			calls++
			if calls%2 == 1 {
				return nil, errors.New("offline")
			}
			return &labpb.MaintenanceWindowList{}, nil
		}, func(context.Context, []infraModel.AgentMaintenanceWindow) error {
			mu.Lock()
			defer mu.Unlock()
			recorded++
			return nil
		})
	}()
	deadline := time.After(5 * time.Second)
	for {
		mu.Lock()
		ok := recorded >= 2
		mu.Unlock()
		if ok {
			break
		}
		select {
		case <-deadline:
			t.Fatal("the poll did not record")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if recorded > calls/2+1 {
		t.Fatalf("recorded %d of %d reads, half of them failed", recorded, calls)
	}
}
