package resourceCalendarUseCase

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

func listed(p calModel.Part, phase, reason string, groups int, endsAt *time.Time) infraModel.ClusterReservation {
	return infraModel.ClusterReservation{ID: p.ID, From: p.Window.Start, To: p.Window.End, Amount: p.Amount,
		Status: &infraModel.ClusterReservationStatus{Phase: phase, EndingReason: reason, Groups: groups, EndsAt: endsAt, Used: Amount{CPUMillicores: 10, MemoryBytes: 20}}}
}

func observeHarness(t *testing.T) (*harness, *fakePort, *fakeListener) {
	t.Helper()
	h, port := syncHarness(t)
	l := &fakeListener{}
	h.uc.SetPartListener(l)
	return h, port, l
}

func TestObserveSnapshotWritesStatus(t *testing.T) {
	h, _, _ := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), agentOne, true, []infraModel.ClusterReservation{listed(p, calModel.PartActive, "", 3, nil)}, nil))
	got, err := h.store.GetPart(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, calModel.PartActive, got.Status.Phase)
	assert.Equal(t, 3, got.Status.Groups)
	assert.Equal(t, Amount{CPUMillicores: 10, MemoryBytes: 20}, got.Status.Used)
}

func TestDriftResyncsWithoutSignal(t *testing.T) {
	h, port, l := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	// The agent holds an earlier end than the calendar (an update was lost) and gives the notice.
	short := listed(p, calModel.PartEnding, EndingWindowClosing, 2, nil)
	short.To = p.Window.End.Add(-time.Hour)
	port.agent(agentOne)[p.ID] = short
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), agentOne, false, []infraModel.ClusterReservation{short}, nil))
	assert.Equal(t, p.Window.End, port.held[agentOne][p.ID].To, "the part is sent again at once")
	assert.Empty(t, l.ending, "drift is not a notice")
	assert.Empty(t, h.notifier.ending)
}

func testRunPart(t *testing.T, h *harness) calModel.Part {
	t.Helper()
	owner := uuid.Must(uuid.NewV7())
	id, _ := admit(t, h, owner, 30*time.Minute, nil)
	p, err := h.store.RunPart(context.Background(), id)
	require.NoError(t, err)
	return p
}

func TestEndingTestRunNotifiesAuthorOnce(t *testing.T) {
	h, _, l := observeHarness(t)
	p := testRunPart(t, h)
	endsAt := p.Window.End
	report := []infraModel.ClusterReservation{listed(p, calModel.PartEnding, EndingWindowClosing, 1, &endsAt)}
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, true, report, nil))
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, true, report, nil))
	require.Len(t, l.ending, 1, "a re-sent snapshot does not repeat the notice")
	assert.Equal(t, p.ID, l.ending[0].Part.ID)
	assert.Equal(t, endsAt, l.ending[0].EndsAt)
	assert.Empty(t, h.notifier.ending, "a test run tells its author, not the admins")
}

func TestEndingEventPartAfterEventEndIsSilent(t *testing.T) {
	h, _, l := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	h.clock = time.Date(2026, 10, 5, 12, 56, 0, 0, time.UTC) // the event ended at 12:00; the tail gap runs to 13:00
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), agentOne, false, []infraModel.ClusterReservation{listed(p, calModel.PartEnding, EndingWindowClosing, 1, nil)}, nil))
	assert.Empty(t, h.notifier.ending, "the expected end of the tail gap tells nobody")
	require.Len(t, l.ending, 1, "the stands still stop and snapshot what they keep")
	assert.True(t, l.ending[0].EventEnded)
	assert.Equal(t, h.event.ID, *l.ending[0].EventID)
}

func TestEndingEventPartDuringEventNotifies(t *testing.T) {
	h, _, l := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	h.clock = time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC) // the event runs
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), agentOne, false, []infraModel.ClusterReservation{listed(p, calModel.PartEnding, EndingTenantGone, 1, nil)}, nil))
	require.Len(t, h.notifier.ending, 1, "the window is too short: admins and organizers are told")
	assert.Equal(t, h.event.ID, h.notifier.ending[0].EventID)
	assert.Equal(t, agentOne, h.notifier.ending[0].AgentID)
	require.Len(t, l.ending, 1)
	assert.False(t, l.ending[0].EventEnded)
}

func TestReducedReservationNotifiesAdmins(t *testing.T) {
	h, _, _ := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentOne]
	report := listed(p, calModel.PartActive, EndingAmountReduced, 2, nil)
	report.Status.Exceeded = true
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), agentOne, false, []infraModel.ClusterReservation{report}, nil))
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), agentOne, false, []infraModel.ClusterReservation{report}, nil))
	assert.Len(t, h.notifier.reduced, 1)
}

func TestEndedPartFinishesTestDeploy(t *testing.T) {
	h, _, l := observeHarness(t)
	p := testRunPart(t, h)
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, false, []infraModel.ClusterReservation{listed(p, calModel.PartEnded, "", 0, nil)}, nil))
	require.Len(t, l.ended, 1)
	assert.Equal(t, *p.TestDeployID, *l.ended[0].Part.TestDeployID)
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, true, []infraModel.ClusterReservation{listed(p, calModel.PartEnded, "", 0, nil)}, nil))
	assert.Len(t, l.ended, 1, "the end acts once")
}

func TestEndedEventPartReleasesBindingsWithoutFailure(t *testing.T) {
	h, _, l := observeHarness(t)
	res := setEvent(t, h, EventReservationInput{})
	p := partsOf(t, h, res.Reservation.ID)[agentTwo]
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), agentTwo, false, []infraModel.ClusterReservation{listed(p, calModel.PartEnded, "", 0, nil)}, nil))
	require.Len(t, l.ended, 1)
	assert.Equal(t, h.event.ID, *l.ended[0].EventID, "the stands release the event's labs on that agent")
	assert.Equal(t, agentTwo, l.ended[0].Part.AgentID)
	alarms, _ := h.store.ListAlarms(context.Background(), true, 10)
	assert.Empty(t, alarms, "the end is not a failure")
}

func TestDeletedKeyActsAsEnded(t *testing.T) {
	h, _, l := observeHarness(t)
	p := testRunPart(t, h)
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, false, nil, []uuid.UUID{p.ID}))
	require.NoError(t, h.uc.ObserveClusterReservations(context.Background(), p.AgentID, false, nil, []uuid.UUID{p.ID}))
	require.Len(t, l.ended, 1)
	got, err := h.store.GetPart(context.Background(), p.ID)
	require.NoError(t, err)
	assert.Equal(t, calModel.PartEnded, got.Status.Phase)
}
