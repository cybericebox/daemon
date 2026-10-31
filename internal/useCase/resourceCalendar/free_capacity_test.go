package resourceCalendarUseCase

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The hint beside an elevation request: the capacity of the used agents less what the reservations hold now.
func TestClusterFreeCapacityIsCapacityLessTheActiveReservations(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	idle, err := h.uc.ClusterFreeCapacity(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(20000), idle.Capacity.CPUMillicores, "two agents of 10 CPU")
	assert.Equal(t, int64(128<<30), idle.Capacity.MemoryBytes)
	assert.Equal(t, idle.Capacity, idle.Free, "nothing is reserved yet")
	assert.False(t, idle.CPUUnlimited)

	_, err = h.uc.SetEventResourceReservation(ctx, h.event.ID, EventReservationInput{}, uuid.Nil)
	require.NoError(t, err)
	h.clock = time.Date(2026, 10, 5, 9, 0, 0, 0, time.UTC) // inside the reservation window
	busy, err := h.uc.ClusterFreeCapacity(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(8000), busy.Allocated.CPUMillicores)
	assert.Equal(t, int64(12000), busy.Free.CPUMillicores)
	assert.Equal(t, int64(128<<30)-int64(8*(2<<30)), busy.Free.MemoryBytes)
}
