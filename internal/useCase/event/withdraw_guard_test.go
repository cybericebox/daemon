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

func TestLabsEndWithReservation(t *testing.T) {
	assert.False(t, (&EventUseCase{}).labsEndWithReservation(), "without a calendar the older teardown stays")
	u := &EventUseCase{}
	u.SetResourceGate(endingGate{})
	assert.False(t, u.labsEndWithReservation(), "a gate that cannot tell about reservations is no calendar")
	u.SetResourceGate(reservationHolderGate{})
	assert.True(t, u.labsEndWithReservation())
}

type reservationHolderGate struct{ endingGate }

func (reservationHolderGate) HasEventReservation(context.Context, uuid.UUID) (bool, error) {
	return true, nil
}
