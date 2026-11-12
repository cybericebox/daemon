package event

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

type shorteningGate struct {
	endingGate
	oldEnd time.Time
	called *time.Time
}

func (g shorteningGate) ShortenEventReservation(_ context.Context, _ uuid.UUID, newEnd time.Time, _ uuid.UUID) (time.Time, error) {
	*g.called = newEnd
	return g.oldEnd, nil
}

// Saving a withdrawal earlier than the reservation end shortens the reservation to it, in the same action; nothing
// to report when the window was not touched, there is no withdrawal or no calendar.
func TestShortenReservationToWithdrawal(t *testing.T) {
	withdraw := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	var called time.Time
	u := &EventUseCase{}
	u.SetResourceGate(shorteningGate{oldEnd: withdraw.Add(time.Hour), called: &called})

	end, _, err := u.shortenReservationToWithdrawal(context.Background(), uuid.Nil, &withdraw, uuid.Nil)
	require.NoError(t, err)
	require.NotNil(t, end)
	assert.True(t, end.Equal(withdraw))
	assert.True(t, called.Equal(withdraw))

	u.SetResourceGate(shorteningGate{called: &called}) // zero old end: the window already ended by then
	end, _, err = u.shortenReservationToWithdrawal(context.Background(), uuid.Nil, &withdraw, uuid.Nil)
	require.NoError(t, err)
	assert.Nil(t, end)

	end, _, err = u.shortenReservationToWithdrawal(context.Background(), uuid.Nil, nil, uuid.Nil)
	require.NoError(t, err)
	assert.Nil(t, end, "no withdrawal")
	end, _, err = (&EventUseCase{}).shortenReservationToWithdrawal(context.Background(), uuid.Nil, &withdraw, uuid.Nil)
	require.NoError(t, err)
	assert.Nil(t, end, "no calendar")
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
