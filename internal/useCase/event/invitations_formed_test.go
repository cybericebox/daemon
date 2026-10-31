package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
)

func joiningEvent(t *testing.T, policy eventModel.JoinPolicy, start time.Time) eventModel.Event {
	t.Helper()
	finish := start.Add(8 * time.Hour)
	lifecycle, err := eventModel.NewLifecycle(policy, start.Add(-48*time.Hour), start, &finish, nil, nil)
	require.NoError(t, err)
	lifecycle.Configured = true
	return eventModel.Event{Lifecycle: lifecycle}
}

// A formed team (the captain's confirmation, or the start of an event without late join) takes no more members from
// invitations; an unformed team of an event with late join keeps taking them after the start.
func TestTeamClosedToInvitees(t *testing.T) {
	start := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	after := start.Add(time.Hour)
	formedAt := after
	unformed, formed := eventTeamModel.EventTeam{}, eventTeamModel.EventTeam{FormedAt: &formedAt}

	rolling := joiningEvent(t, eventModel.JoinPolicyRolling, start)
	assert.False(t, teamClosedToInvitees(unformed, rolling, after), "late join: forms only by the captain")
	assert.True(t, teamClosedToInvitees(formed, rolling, after), "the captain marked it formed")

	locked := joiningEvent(t, eventModel.JoinPolicyLockedAtStart, start)
	assert.False(t, teamClosedToInvitees(unformed, locked, start.Add(-time.Minute)), "before the start")
	assert.True(t, teamClosedToInvitees(unformed, locked, after), "formed by the start")
}
