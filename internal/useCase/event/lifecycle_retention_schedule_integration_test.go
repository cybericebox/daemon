package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabGroupRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Real database/use-case regression. Native observations use the existing
// explicit adapters; actual native qualification belongs to the runtime proof.
func TestScheduleExtendedFinishKeepsSolvedWholeEventGenerationUntilNewDeadline(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	// Existing stand fixtures backdate publication beyond their availability
	// window. Repair only that synthetic input before exercising the real API.
	_, err := f.db.Pool.Exec(ctx, `UPDATE events SET available_from=publish_at-interval '1 hour' WHERE id=$1`, f.eventID)
	require.NoError(t, err)
	before, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	require.NotNil(t, before.RetentionUntil)
	oldDeadline := *before.RetentionUntil
	cfg, err := eventConfigRepo.New(f.db.Queries).Get(ctx, f.eventID)
	require.NoError(t, err)
	changedTTL := before.RetentionMinutes + 120
	_, err = f.uc.UpdateEventConfig(ctx, f.eventID, event.UpdateConfigInput{
		Registration: cfg.Registration, ScoreboardVisibility: cfg.ScoreboardVisibility,
		ParticipantsVisibility: cfg.ParticipantsVisibility, MaxTeamSize: cfg.MaxTeamSize,
		MinTeamSize: cfg.MinTeamSize, MaxTeams: cfg.MaxTeams,
		LabPolicy: &event.UpdateLabPolicyInput{RetentionMinutes: &changedTTL},
	}, f.ownerID)
	require.NoError(t, err)
	e, err := eventRepo.New(f.db.Queries).GetByID(ctx, f.eventID)
	require.NoError(t, err)
	finish := oldDeadline.UTC().Add(2 * time.Hour)
	withdraw := finish.Add(time.Hour)
	f.uc.SetLifecycleControls(true)
	_, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{
		JoinPolicy: e.Lifecycle.JoinPolicy, PublishAt: e.Lifecycle.PublishAt,
		StartAt: e.Lifecycle.StartAt, FinishAt: &finish, WithdrawAt: &withdraw,
	}, f.ownerID)
	require.NoError(t, err)
	for i, challenge := range set.challenges {
		submitStoredFlag(t, f, user, challenge, at.Add(time.Duration(i)*time.Millisecond))
	}
	stopped := waveStopped(t, f, before.ID, "solved")
	require.NoError(t, f.uc.ReconcileLabRetention(ctx, oldDeadline.Add(time.Second)))
	after, err := eventLabRepo.New(f.db.Queries).Get(ctx, before.ID)
	require.NoError(t, err)
	require.Equal(t, "Stopped", after.DesiredState, "expired birth deadline must not retire a solved generation while extended event remains open")
	require.Equal(t, finish.Add(time.Duration(before.RetentionMinutes)*time.Minute), after.RetentionUntil.UTC())
	require.Equal(t, before.RetentionMinutes, after.RetentionMinutes, "generation TTL must not follow edited event policy")
	require.Equal(t, stopped.Revision, after.Revision)
	require.Equal(t, stopped.OperationID, after.OperationID)
	require.Equal(t, stopped.Ref, after.Ref)
	require.Equal(t, stopped.AgentUID, after.AgentUID)
	require.Equal(t, "solved", after.CloseReason)
	require.Equal(t, stopped.Allocation, after.Allocation)
}

func retentionScheduleFixture(t *testing.T) (*standFixture, sharedSet) {
	t.Helper()
	f := newStandFixture(t)
	set := f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	certifyLifecycleFixture(t, f)
	_, err := f.db.Pool.Exec(context.Background(), `UPDATE events SET available_from=publish_at-interval '1 hour' WHERE id=$1`, f.eventID)
	require.NoError(t, err)
	seedRetentionGroupLedgers(t, f)
	f.uc.SetLifecycleControls(true)
	return f, set
}

func seedRetentionGroupLedgers(t *testing.T, f *standFixture) {
	t.Helper()
	ctx := context.Background()
	rows, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	require.NoError(t, err)
	seen := map[uuid.UUID]bool{}
	for _, row := range rows {
		if seen[row.EventTeamID] {
			continue
		}
		seen[row.EventTeamID] = true
		sizes, known := f.agent.GroupSizes(infraModel.GroupPlan{})
		require.True(t, known)
		require.NoError(t, eventLabAllocationRepo.New(f.db.Queries).CreateGroup(ctx, eventLabAllocationRepo.Group{TeamID: row.EventTeamID, EventID: f.eventID, Name: row.LabGroupName, Sizes: sizes, CreatedAt: time.Now().UTC()}))
	}
}
func extendRetentionSchedule(t *testing.T, f *standFixture, finish time.Time) error {
	t.Helper()
	e, err := eventRepo.New(f.db.Queries).GetByID(context.Background(), f.eventID)
	if err != nil {
		return err
	}
	withdraw := finish.Add(time.Hour)
	_, err = f.uc.UpdateEventLifecycle(context.Background(), f.eventID, event.UpdateLifecycleInput{JoinPolicy: e.Lifecycle.JoinPolicy, PublishAt: e.Lifecycle.PublishAt, StartAt: e.Lifecycle.StartAt, FinishAt: &finish, WithdrawAt: &withdraw}, f.ownerID)
	return err
}

type retentionFailureQueries struct {
	event.IRepository
	failure error
}

func (q retentionFailureQueries) RefreshEventLabProtectedUntil(context.Context, uuid.UUID) error {
	return q.failure
}

type retentionFailureUOW struct {
	inner   postgres.IUnitOfWorker[event.IRepository]
	failure error
}

func (w retentionFailureUOW) UnitOfWork(ctx context.Context) (context.Context, event.IRepository, postgres.UoW, error) {
	tx, q, u, err := w.inner.UnitOfWork(ctx)
	if err != nil {
		return tx, q, u, err
	}
	return tx, retentionFailureQueries{q, w.failure}, u, nil
}
func retentionUseCase(f *standFixture, q event.IRepository, uow postgres.IUnitOfWorker[event.IRepository]) *event.EventUseCase {
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: uow, Infra: f.agent, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets}, StandPrewarmLead: 30 * time.Minute, StandDeployBudget: 200})
	uc.SetLifecycleControls(true)
	return uc
}
func TestScheduleRetentionPinsAndDeadlineRollbackAtomically(t *testing.T) {
	f, set := retentionScheduleFixture(t)
	ctx := context.Background()
	old, err := eventRepo.New(f.db.Queries).GetByID(ctx, f.eventID)
	require.NoError(t, err)
	l, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	first, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Origin", OpensAt: old.Lifecycle.StartAt, ClosesAt: old.Lifecycle.StartAt.Add(time.Hour), Returnable: true})
	require.NoError(t, err)
	future, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Future", OpensAt: old.Lifecycle.StartAt.Add(time.Hour), ClosesAt: *old.Lifecycle.FinishAt, Returnable: false})
	require.NoError(t, err)
	_ = first
	require.NoError(t, f.uc.SelectRetainedLabsForStage(ctx, f.eventID, future.ID, []uuid.UUID{l.ID}))
	var pinBefore time.Time
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT needed_until FROM event_lab_retention_pins WHERE lab_id=$1 AND stage_id=$2`, l.ID, future.ID).Scan(&pinBefore))
	before, err := eventLabRepo.New(f.db.Queries).Get(ctx, l.ID)
	require.NoError(t, err)
	failure := errors.New("retention pin update rejected")
	f.uc = retentionUseCase(f, f.db.Queries, retentionFailureUOW{postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), failure})
	require.ErrorIs(t, extendRetentionSchedule(t, f, old.Lifecycle.FinishAt.UTC().Add(2*time.Hour)), failure)
	afterEvent, err := eventRepo.New(f.db.Queries).GetByID(ctx, f.eventID)
	require.NoError(t, err)
	require.True(t, old.Lifecycle.FinishAt.Equal(*afterEvent.Lifecycle.FinishAt))
	require.True(t, old.UpdatedAt.Equal(afterEvent.UpdatedAt))
	after, err := eventLabRepo.New(f.db.Queries).Get(ctx, l.ID)
	require.NoError(t, err)
	require.Equal(t, before, after)
	var pinAfter time.Time
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT needed_until FROM event_lab_retention_pins WHERE lab_id=$1 AND stage_id=$2`, l.ID, future.ID).Scan(&pinAfter))
	require.True(t, pinBefore.Equal(pinAfter))
	stages, err := f.uc.ListEventStages(ctx, f.eventID)
	require.NoError(t, err)
	require.True(t, future.ClosesAt.Equal(stages[len(stages)-1].ClosesAt))
}
func TestScheduleRetentionKeepsStagePinsWhenOnlyEventFinishMoves(t *testing.T) {
	f, set := retentionScheduleFixture(t)
	ctx := context.Background()
	e, err := eventRepo.New(f.db.Queries).GetByID(ctx, f.eventID)
	require.NoError(t, err)
	l, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	_, err = f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Origin", OpensAt: e.Lifecycle.StartAt, ClosesAt: e.Lifecycle.StartAt.Add(time.Hour), Returnable: true})
	require.NoError(t, err)
	ttl := int32(15)
	future, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Future", OpensAt: e.Lifecycle.StartAt.Add(time.Hour), ClosesAt: *e.Lifecycle.FinishAt, Returnable: false, LabRetentionMinutes: &ttl})
	require.NoError(t, err)
	require.NoError(t, f.uc.SelectRetainedLabsForStage(ctx, f.eventID, future.ID, []uuid.UUID{l.ID}))
	finish := e.Lifecycle.FinishAt.UTC().Add(2 * time.Hour)
	require.NoError(t, extendRetentionSchedule(t, f, finish))
	var until time.Time
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT needed_until FROM event_lab_retention_pins WHERE lab_id=$1 AND stage_id=$2`, l.ID, future.ID).Scan(&until))
	require.True(t, future.ClosesAt.Add(15*time.Minute).Equal(until))
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, l.ID)
	require.NoError(t, err)
	require.True(t, finish.Add(time.Duration(l.RetentionMinutes)*time.Minute).Equal(*current.RetentionUntil))
	require.Equal(t, l.Revision, current.Revision)
	require.Equal(t, l.OperationID, current.OperationID)
}

// Labs live until the reservation window ends (owner decision 2026-10-19): a stopped lab and a stopped group whose
// retention deadline passed are kept, the deadline is ignored.
func TestRetentionDeadlineNeverRetires(t *testing.T) {
	f, set := retentionScheduleFixture(t)
	ctx := context.Background()
	l, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	l = waveStopped(t, f, l.ID, "solved")
	require.NotNil(t, l.RetentionUntil)
	f.uc.SetLifecycleControls(true)
	require.NoError(t, f.uc.ReconcileLabRetention(ctx, l.RetentionUntil.Add(30*24*time.Hour)))
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, l.ID)
	require.NoError(t, err)
	require.Equal(t, "Stopped", current.DesiredState, "a passed retention deadline never retires a lab")
	require.Nil(t, current.RetirementStopTarget)
	require.Equal(t, l.Revision, current.Revision)
	require.Equal(t, l.Allocation, current.Allocation)
}
func TestScheduleRetentionStoppedGroupCannotExpireBeforeRenewedChildren(t *testing.T) {
	f, _ := allocationFixture(t, 0)
	seedRetentionGroupLedgers(t, f)
	ctx := context.Background()
	_, err := f.db.Pool.Exec(ctx, `UPDATE events SET available_from=publish_at-interval '1 hour' WHERE id=$1`, f.eventID)
	require.NoError(t, err)
	rows, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	require.NoError(t, err)
	var oldDeadline time.Time
	for _, row := range rows {
		if row.EventTeamID == f.blueID {
			l := waveStopped(t, f, row.ID, "solved")
			oldDeadline = *l.RetentionUntil
		}
	}
	repo := eventLabGroupRepo.New(f.db.Queries)
	g, err := repo.Get(ctx, f.blueID)
	require.NoError(t, err)
	expected := g.Revision
	g.AgentUID = "fixture-group"
	g.AgentGeneration = 1
	g.Revision++
	g.ObservedRevision = g.Revision
	g.OperationID = uuid.Must(uuid.NewV7())
	g.DesiredState = "Stopped"
	g.ActualState = "Stopped"
	g.AccessFenced = true
	g.RetentionUntil = &oldDeadline
	at := time.Now().UTC()
	g.ObservedAt = &at
	g.Allocation = eventLabModel.Allocation{RuntimeState: "Released", ReleasedAt: &at, ObservedAt: &at, StorageState: "None"}
	g.UpdatedAt = at
	ok, err := repo.Update(ctx, g, expected)
	require.NoError(t, err)
	require.True(t, ok)
	f.uc.SetLifecycleControls(true)
	require.NoError(t, extendRetentionSchedule(t, f, oldDeadline.UTC().Add(2*time.Hour)))
	require.NoError(t, f.uc.ReconcileLabRetention(ctx, oldDeadline.Add(time.Second)))
	after, err := repo.Get(ctx, f.blueID)
	require.NoError(t, err)
	require.Equal(t, "Stopped", after.DesiredState)
	require.Equal(t, g.Revision, after.Revision)
	require.Equal(t, g.OperationID, after.OperationID)
}
