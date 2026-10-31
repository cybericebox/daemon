package resourceCalendarUseCase

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

func scheduledEvent(t *testing.T, finish, withdraw time.Time) eventModel.Event {
	t.Helper()
	start := finish.Add(-4 * time.Hour)
	lifecycle, err := eventModel.NewLifecycle(eventModel.JoinPolicyLockedAtStart, start.Add(-time.Hour), start, &finish, &withdraw, nil)
	require.NoError(t, err)
	return eventModel.Event{Lifecycle: lifecycle}
}

// The window of an event may not end after its withdrawal; the default end (event end + tail) is cut at the
// withdrawal, a fixed end beyond it is refused with both times.
func TestEventWindowStaysBeforeTheWithdrawal(t *testing.T) {
	finish := time.Date(2026, 11, 1, 18, 0, 0, 0, time.UTC)
	withdraw := finish.Add(30 * time.Minute) // shorter than the 1h tail gap
	e := scheduledEvent(t, finish, withdraw)
	u := &ResourceCalendarUseCase{cfg: Config{TailGap: time.Hour}}

	window, _, err := u.eventWindow(e, EventReservationInput{}, time.Hour, time.Hour)
	require.NoError(t, err)
	assert.True(t, window.End.Equal(withdraw), "the default end is cut at the withdrawal: %s", window.End)

	beyond := withdraw.Add(time.Minute)
	_, _, err = u.eventWindow(e, EventReservationInput{WindowEnd: &beyond}, time.Hour, time.Hour)
	require.ErrorIs(t, err, calModel.ErrReservationEndsAfterWithdraw.Err())

	within := withdraw.Add(-time.Minute)
	_, _, err = u.eventWindow(e, EventReservationInput{WindowEnd: &within}, time.Hour, time.Hour)
	require.NoError(t, err)

	noWithdraw := eventModel.Event{Lifecycle: func() eventModel.Lifecycle {
		l, lErr := eventModel.NewLifecycle(eventModel.JoinPolicyLockedAtStart, finish.Add(-6*time.Hour), finish.Add(-4*time.Hour), &finish, nil, nil)
		require.NoError(t, lErr)
		return l
	}()}
	window, _, err = u.eventWindow(noWithdraw, EventReservationInput{}, time.Hour, time.Hour)
	require.NoError(t, err)
	assert.True(t, window.End.Equal(finish.Add(time.Hour)))
}
