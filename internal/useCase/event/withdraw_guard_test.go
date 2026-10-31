package event

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

type endingGate struct {
	end   time.Time
	found bool
}

func (endingGate) HoldsForAllTeams(context.Context, uuid.UUID, resourcesModel.Amount, int, resourcesModel.Amount) error {
	return nil
}
func (g endingGate) EventReservationEnd(context.Context, uuid.UUID) (time.Time, bool, error) {
	return g.end, g.found, nil
}

// The event cannot be withdrawn before its reservation ends; with no reservation, no withdrawal or no calendar there
// is nothing to protect.
func TestRequireWithdrawAfterReservation(t *testing.T) {
	end := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	withdrawAt := func(d time.Duration) *time.Time { w := end.Add(d); return &w }
	u := &EventUseCase{}
	u.SetResourceGate(endingGate{end: end, found: true})

	require.NoError(t, u.requireWithdrawAfterReservation(context.Background(), uuid.Nil, withdrawAt(0)), "at the end is fine")
	require.NoError(t, u.requireWithdrawAfterReservation(context.Background(), uuid.Nil, withdrawAt(time.Hour)))
	require.NoError(t, u.requireWithdrawAfterReservation(context.Background(), uuid.Nil, nil), "no withdrawal")

	err := u.requireWithdrawAfterReservation(context.Background(), uuid.Nil, withdrawAt(-time.Minute))
	require.ErrorIs(t, err, calModel.ErrWithdrawBeforeReservationEnd.Err())

	u.SetResourceGate(endingGate{found: false})
	require.NoError(t, u.requireWithdrawAfterReservation(context.Background(), uuid.Nil, withdrawAt(-time.Hour)), "no reservation")
	require.NoError(t, (&EventUseCase{}).requireWithdrawAfterReservation(context.Background(), uuid.Nil, withdrawAt(-time.Hour)), "no calendar")
}

type holderGate struct {
	endingGate
	reserved bool
}

func (g holderGate) HasEventReservation(context.Context, uuid.UUID) (bool, error) {
	return g.reserved, nil
}

// The labs of an event end with its reservation only while it has an active one that ends by the withdrawal;
// anything else keeps the withdrawal teardown.
func TestLabsEndWithReservation(t *testing.T) {
	withdraw := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	decide := func(gate ResourceGate, withdrawAt *time.Time) bool {
		u := &EventUseCase{}
		if gate != nil {
			u.SetResourceGate(gate)
		}
		return u.labsEndWithReservation(context.Background(), uuid.Nil, withdrawAt)
	}
	assert.False(t, decide(nil, &withdraw), "no calendar: the older teardown stays")
	assert.False(t, decide(endingGate{}, &withdraw), "a gate that cannot tell about reservations is no calendar")
	assert.False(t, decide(holderGate{reserved: false}, &withdraw), "no or a cancelled reservation")
	assert.True(t, decide(holderGate{endingGate: endingGate{end: withdraw.Add(-time.Hour), found: true}, reserved: true}, &withdraw))
	assert.True(t, decide(holderGate{endingGate: endingGate{end: withdraw, found: true}, reserved: true}, &withdraw))
	assert.False(t, decide(holderGate{endingGate: endingGate{end: withdraw.Add(time.Minute), found: true}, reserved: true}, &withdraw), "outlives the withdrawal")
	assert.True(t, decide(holderGate{endingGate: endingGate{end: withdraw.Add(time.Hour), found: true}, reserved: true}, nil), "no withdrawal: nothing to compare")
}
