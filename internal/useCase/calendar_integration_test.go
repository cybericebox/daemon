package useCase

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	calendarUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
)

type fixedAgents []infraModel.AgentRecord

func (f fixedAgents) ListRecords(context.Context) ([]infraModel.AgentRecord, error) { return f, nil }

type fixedPlanner struct{ need calendarUseCase.Need }

func (f fixedPlanner) ReservationNeed(context.Context, uuid.UUID) (calendarUseCase.Need, error) {
	return f.need, nil
}

// The calendar runs against a real database: the transaction wrapper with the calendar lock, an event
// reservation with its placement, a change request approved, a test lab booking and the readiness check.
func TestResourceCalendarAgainstARealDatabase(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)

	creator, err := userRepo.New(db.Queries).Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "calendar@test.test", now))
	require.NoError(t, err)
	ev, err := eventModel.NewEvent("calendar", "Calendar event", now, now.Add(30*24*time.Hour), creator.ID, now)
	require.NoError(t, err)
	created, err := eventRepo.New(db.Queries).Create(ctx, ev)
	require.NoError(t, err)

	seen := now
	cpu, mem := int64(10000), int64(64<<30)
	agentA := infraModel.AgentRecord{AgentRegistration: infraModel.AgentRegistration{ID: uuid.Must(uuid.NewV7()), Name: "a", Enabled: true, CapacityCPUMillicores: &cpu, CapacityMemoryBytes: &mem, CapacitySeenAt: &seen}}
	agents := &fixedAgents{agentA}
	uc := calendarUseCase.New(calendarUseCase.Dependencies{
		Store: resourceCalendarRepo.New(db.Queries), Tx: calendarTx{uow: postgres.NewUnitOfWorker[resourceCalendarRepo.Queries](postgres.NewUoWFactory(db.Pool))},
		Agents: agents, Events: eventRepo.New(db.Queries), Configs: eventConfigRepo.New(db.Queries),
		Planner: fixedPlanner{need: calendarUseCase.Need{Teams: 4, PerTeam: calendarUseCase.Amount{CPUMillicores: 1000, MemoryBytes: 2 << 30}}},
		Config:  calendarUseCase.DefaultConfig(), Now: func() time.Time { return now },
	})

	start, end := now.Add(time.Hour), now.Add(5*time.Hour)
	res, err := uc.SetEventResourceReservation(ctx, created.ID, calendarUseCase.EventReservationInput{WindowStart: &start, WindowEnd: &end}, creator.ID)
	require.NoError(t, err)
	assert.True(t, res.Saved)
	assert.True(t, res.Reservation.Covered)
	assert.Equal(t, end.Add(time.Hour), res.Reservation.To, "the tail gap is added to the event end")
	require.Len(t, res.Reservation.Placement, 1)
	assert.Equal(t, 4, res.Reservation.Placement[0].Units)

	got, err := uc.GetEventResourceReservation(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, res.Reservation.Size, got.Reservation.Size)
	tl, err := uc.GetResourceCalendarTimeline(ctx, now, now.Add(12*time.Hour))
	require.NoError(t, err)
	require.Len(t, tl.Reservations, 1)
	assert.Equal(t, "Calendar event", tl.Reservations[0].EventName)
	assert.Empty(t, tl.Conflicts)

	// A change request: extend the window; the admin approves; the reservation grows by the longer window.
	longer := end.Add(2 * time.Hour)
	organizer := creator.ID
	ch, err := uc.RequestResourceChange(ctx, created.ID, organizer, calendarUseCase.ChangeInput{WindowEnd: &longer, Reason: "runs longer"})
	require.NoError(t, err)
	_, err = uc.DecideResourceChangeRequest(ctx, ch.ID, true, "ok", creator.ID, false)
	require.NoError(t, err)
	got, err = uc.GetEventResourceReservation(ctx, created.ID)
	require.NoError(t, err)
	assert.Equal(t, longer.Add(time.Hour), got.Reservation.To)
	org, err := uc.GetEventResources(ctx, created.ID)
	require.NoError(t, err)
	assert.True(t, org.Reserved)
	require.Len(t, org.Changes, 1)
	assert.Equal(t, calModel.ChangeApproved, org.Changes[0].Status)

	// The agent disappears: the readiness check raises an alarm per cause once and does not move the teams.
	*agents = fixedAgents{}
	require.NoError(t, uc.ReconcileResourceCalendar(ctx))
	require.NoError(t, uc.ReconcileResourceCalendar(ctx))
	alarms, err := uc.ListResourceAlarms(ctx, true)
	require.NoError(t, err)
	kinds := map[calModel.AlarmKind]int{}
	for _, a := range alarms {
		kinds[a.Kind]++
	}
	assert.Equal(t, 1, kinds[calModel.AlarmAgentLost], "one alarm per cause, not one per pass")
	// The agent returns: the alarm resolves.
	*agents = fixedAgents{agentA}
	require.NoError(t, uc.ReconcileResourceCalendar(ctx))
	alarms, err = uc.ListResourceAlarms(ctx, true)
	require.NoError(t, err)
	assert.Empty(t, alarms)

	// A test lab booking after the event, then its cancellation.
	b, err := uc.BookTestLab(ctx, creator.ID, longer.Add(2*time.Hour), 2*time.Hour, calendarUseCase.Amount{CPUMillicores: 500, MemoryBytes: 1 << 30}, calendarUseCase.Amount{})
	require.NoError(t, err)
	list, err := uc.ListTestLabBookings(ctx, creator.ID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.NoError(t, uc.CancelTestLabBooking(ctx, creator.ID, b.ID))

	// A test lab is admitted inside the pool and its hold is released.
	_, _, err = uc.SetResourceTestPool(ctx, calendarUseCase.Amount{CPUMillicores: 1000, MemoryBytes: 2 << 30}, false)
	require.NoError(t, err)
	lab := uuid.Must(uuid.NewV7())
	room, err := uc.AdmitTestLab(ctx, calendarUseCase.TestLabRequest{ID: lab, Owner: creator.ID, Size: calendarUseCase.Amount{CPUMillicores: 500, MemoryBytes: 1 << 30}})
	require.NoError(t, err)
	assert.Equal(t, calModel.ViaPool, room.Via)
	stats, err := uc.GetResourceCalendarStats(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(500), stats.TestLabsHeld.CPUMillicores)
	require.NoError(t, uc.ReleaseTestLab(ctx, lab))
}
