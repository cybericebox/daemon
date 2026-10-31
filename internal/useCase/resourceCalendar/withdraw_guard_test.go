package resourceCalendarUseCase

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

func scheduled(t *testing.T, start, finish time.Time, withdraw *time.Time) eventModel.Event {
	t.Helper()
	lifecycle, err := eventModel.NewLifecycle(eventModel.JoinPolicyLockedAtStart, start.Add(-24*time.Hour), start, &finish, withdraw, nil)
	require.NoError(t, err)
	return eventModel.Event{Lifecycle: lifecycle}
}

// The window of an event's reservation: it starts at least the lead before the event, ends no earlier than the event
// and no later than its withdrawal; the default is exactly the minimum lead and the tail after the event, cut at the
// withdrawal.
func TestEventWindowBounds(t *testing.T) {
	start := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	finish := start.Add(4 * time.Hour)
	withdraw := finish.Add(30 * time.Minute) // shorter than the tail
	u := &ResourceCalendarUseCase{cfg: Config{TailGap: time.Hour, LeadMargin: 30 * time.Minute}}
	lead := u.minLead()
	assert.Equal(t, time.Hour, lead, "the defaults are 60 minutes before the start")

	t.Run("default", func(t *testing.T) {
		e := scheduled(t, start, finish, &withdraw)
		w, tail, err := u.eventWindow(e, EventReservationInput{}, lead, time.Hour)
		require.NoError(t, err)
		assert.True(t, w.Start.Equal(start.Add(-time.Hour)))
		assert.True(t, w.End.Equal(withdraw), "cut at the withdrawal")
		assert.Equal(t, 30*time.Minute, tail)

		noWithdraw := scheduled(t, start, finish, nil)
		w, _, err = u.eventWindow(noWithdraw, EventReservationInput{}, lead, time.Hour)
		require.NoError(t, err)
		assert.True(t, w.End.Equal(finish.Add(time.Hour)))
	})
	t.Run("default end with a withdrawal off the slot grid", func(t *testing.T) {
		odd := finish.Add(37 * time.Minute)
		e := scheduled(t, start, finish, &odd)
		w, _, err := u.eventWindow(e, EventReservationInput{}, lead, time.Hour)
		require.NoError(t, err)
		assert.True(t, w.End.Equal(finish.Add(30*time.Minute)), "rounded down to the slot, never past the withdrawal: %s", w.End)
	})
	t.Run("start", func(t *testing.T) {
		e := scheduled(t, start, finish, &withdraw)
		earlier, later := start.Add(-2*time.Hour), start.Add(-30*time.Minute)
		_, _, err := u.eventWindow(e, EventReservationInput{WindowStart: &earlier}, lead, time.Hour)
		require.NoError(t, err, "earlier is allowed")
		_, _, err = u.eventWindow(e, EventReservationInput{WindowStart: &later}, lead, time.Hour)
		require.ErrorIs(t, err, calModel.ErrReservationStartTooLate.Err())
		exact := start.Add(-time.Hour)
		_, _, err = u.eventWindow(e, EventReservationInput{WindowStart: &exact}, lead, time.Hour)
		require.NoError(t, err, "exactly the lead is allowed")
	})
	t.Run("end", func(t *testing.T) {
		e := scheduled(t, start, finish, &withdraw)
		atFinish, beforeFinish, afterWithdraw := finish, finish.Add(-16*time.Minute), withdraw.Add(time.Minute)
		_, _, err := u.eventWindow(e, EventReservationInput{WindowEnd: &atFinish}, lead, time.Hour)
		require.NoError(t, err, "exactly the event end is allowed: the tail is only a default")
		_, _, err = u.eventWindow(e, EventReservationInput{WindowEnd: &beforeFinish}, lead, time.Hour)
		require.ErrorIs(t, err, calModel.ErrReservationEndsBeforeEventEnd.Err())
		_, _, err = u.eventWindow(e, EventReservationInput{WindowEnd: &afterWithdraw}, lead, time.Hour)
		require.ErrorIs(t, err, calModel.ErrReservationEndsAfterWithdraw.Err())
		atWithdraw := withdraw
		_, _, err = u.eventWindow(e, EventReservationInput{WindowEnd: &atWithdraw}, lead, time.Hour)
		require.NoError(t, err)
	})
	t.Run("a zero tail is allowed", func(t *testing.T) {
		e := scheduled(t, start, finish, nil)
		w, tail, err := u.eventWindow(e, EventReservationInput{}, lead, 0)
		require.NoError(t, err)
		assert.True(t, w.End.Equal(finish))
		assert.Zero(t, tail)
	})
}
