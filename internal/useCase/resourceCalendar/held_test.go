package resourceCalendarUseCase

import (
	"context"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestHeldUnknownDoesNotBecomeFreeAtCalendarBoundary(t *testing.T) {
	h := newHarness(t)
	h.usage.u = Usage{ByEvent: map[uuid.UUID]Amount{h.event.ID: {CPUMillicores: 750, MemoryBytes: 512 << 20}}, StorageByEvent: map[uuid.UUID]eventLabModel.StorageBudget{h.event.ID: {SnapshotQuotaBytes: 1 << 30}}}
	require.Error(t, h.uc.requireHeldCoverage(context.Background(), h.store, h.clock, nil))
	r := &calModel.Reservation{EventID: &h.event.ID, Teams: 1, Size: Amount{CPUMillicores: 1000, MemoryBytes: 1 << 30}, SizeSnapshotQuotaBytes: 1 << 30, Window: calModel.Window{Start: h.clock.Add(-time.Hour), End: h.clock.Add(time.Hour)}, Placement: []calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 1}}}
	require.NoError(t, h.uc.requireHeldCoverage(context.Background(), h.store, h.clock, r))
	r.Window.End = h.clock
	require.Error(t, h.uc.requireHeldCoverage(context.Background(), h.store, h.clock, r))
}
