package event_test

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	eventLab "github.com/cybericebox/daemon/internal/model/eventLab"
	cal "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	use "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

// InitialDeploymentAbsent is an actual producer port; the test exercises the exported use case.
func (a *standAgent) InitialDeploymentAbsent(_ context.Context, ref eventLab.Ref) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.statusErr != nil {
		return false, a.statusErr
	}
	_, born := a.topos[ref.Group+"/"+ref.Lab]
	return !born, nil
}
func prepareNeverCreatedFailure(t *testing.T) (*standFixture, sharedSet, eventLab.Lab) {
	t.Helper()
	f := newStandFixture(t)
	set := f.attachSharedSet(t)
	now := time.Now().UTC()
	r, e := cal.NewEventReservation(cal.EventInput{EventID: f.eventID, Window: cal.Window{Start: now.Add(-time.Hour), End: now.Add(3 * time.Hour)}, Teams: 3, PerTeam: cal.Amount{CPUMillicores: 2000, MemoryBytes: 2 << 30}}, f.ownerID, now)
	require.NoError(t, e)
	r.SetPlacement([]cal.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 3}}, 0, now)
	require.NoError(t, resourceCalendarRepo.New(f.db.Queries).CreateReservation(context.Background(), r))
	f.uc.SetLifecycleControls(true)
	f.agent.preGroupCreateError = errors.New("producer refused service size")
	f.pass(t)
	lab, e := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	require.NoError(t, e)
	require.Empty(t, lab.AgentUID)
	require.Nil(t, lab.CreateEvidence)
	var failed int
	require.NoError(t, f.db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM lab_bindings WHERE event_team_id=$1 AND readiness=2", f.blueID).Scan(&failed))
	require.Greater(t, failed, 0)
	return f, set, lab
}
func TestInitialFailedRetryPreservesCanonicalPinsAndIntent(t *testing.T) {
	f, set, before := prepareNeverCreatedFailure(t)
	ctx := context.Background()
	f.agent.preGroupCreateError = nil
	_, e := f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, f.ownerID)
	require.NoError(t, e)
	after, e := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, e)
	require.Equal(t, before.ID, after.ID)
	require.Equal(t, before.Ref, after.Ref)
	require.Equal(t, before.DefinitionVersionID, after.DefinitionVersionID)
	require.Equal(t, before.DefinitionHash, after.DefinitionHash)
	require.Equal(t, before.OperationID, after.OperationID)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.Generation, after.Generation)
	require.Nil(t, after.ClosedAt)
	require.Equal(t, "Running", after.DesiredState)
	var failed int
	require.NoError(t, f.db.Pool.QueryRow(ctx, "SELECT count(*) FROM lab_bindings WHERE event_team_id=$1 AND readiness=2", f.blueID).Scan(&failed))
	require.Zero(t, failed)
	var pins int
	require.NoError(t, f.db.Pool.QueryRow(ctx, "SELECT count(*) FROM event_lab_objectives WHERE lab_id=$1", before.ID).Scan(&pins))
	require.Equal(t, 3, pins)
}
func TestConcurrentInitialFailedRetryKeepsSamePins(t *testing.T) {
	f, set, before := prepareNeverCreatedFailure(t)
	f.agent.preGroupCreateError = nil
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, e := f.uc.RecreateTeamStand(context.Background(), f.eventID, f.blueID, f.ownerID)
			errs <- e
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		require.NoError(t, e)
	}
	after, e := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	require.NoError(t, e)
	require.Equal(t, before.ID, after.ID)
	require.Equal(t, before.OperationID, after.OperationID)
	require.Equal(t, before.Revision, after.Revision)
}
func TestInitialFailedRetryRefusesLateNativeBirth(t *testing.T) {
	f, set, before := prepareNeverCreatedFailure(t)
	f.agent.preGroupCreateError = nil
	f.agent.mu.Lock()
	f.agent.topos[before.Ref.Group+"/"+before.Ref.Lab] = f.sets.topology[before.DefinitionVersionID]
	f.agent.mu.Unlock()
	_, e := f.uc.RecreateTeamStand(context.Background(), f.eventID, f.blueID, f.ownerID)
	require.Error(t, e)
	after, e := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	require.NoError(t, e)
	require.Equal(t, before.ID, after.ID)
	require.Equal(t, before.OperationID, after.OperationID)
	require.Nil(t, after.ClosedAt)
}

func TestInitialFailedRetryUnknownProducerPreservesFailure(t *testing.T) {
	f, set, before := prepareNeverCreatedFailure(t)
	f.agent.statusErr = errors.New("producer unavailable")
	_, e := f.uc.RecreateTeamStand(context.Background(), f.eventID, f.blueID, f.ownerID)
	require.Error(t, e)
	after, e := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	require.NoError(t, e)
	require.Equal(t, before.ID, after.ID)
	require.Nil(t, after.ClosedAt)
	var n int
	require.NoError(t, f.db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM lab_bindings WHERE event_team_id=$1 AND readiness=2", f.blueID).Scan(&n))
	require.Greater(t, n, 0)
}
func TestInitialFailedRetrySupersededDefinitionPreservesPins(t *testing.T) {
	f, set, before := prepareNeverCreatedFailure(t)
	_, e := f.db.Pool.Exec(context.Background(), "UPDATE event_exercises SET status=2 WHERE id=$1", set.exerciseID)
	require.NoError(t, e)
	_, e = f.uc.RecreateTeamStand(context.Background(), f.eventID, f.blueID, f.ownerID)
	require.Error(t, e)
	after, e := eventLabRepo.New(f.db.Queries).Get(context.Background(), before.ID)
	require.NoError(t, e)
	require.Equal(t, before.OperationID, after.OperationID)
	require.Nil(t, after.ClosedAt)
}
func TestInitialFailedRetryForeignTeamCannotChangePins(t *testing.T) {
	f, set, before := prepareNeverCreatedFailure(t)
	other := uuid.Must(uuid.NewV7())
	_, e := f.uc.RecreateTeamStand(context.Background(), other, f.blueID, f.ownerID)
	require.Error(t, e)
	after, e := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	require.NoError(t, e)
	require.Equal(t, before.ID, after.ID)
	require.Nil(t, after.ClosedAt)
}

func TestInitialFailedRetryLateDBEvidenceRefusesPinLoss(t *testing.T) {
	for _, field := range []string{"evidence", "uid", "revision", "closed"} {
		t.Run(field, func(t *testing.T) {
			f, set, before := prepareNeverCreatedFailure(t)
			ctx := context.Background()
			query := ""
			switch field {
			case "evidence":
				query = "UPDATE event_team_labs SET create_evidence='{}'::jsonb WHERE id=$1"
			case "uid":
				query = "UPDATE event_team_labs SET agent_uid='late-native',agent_generation=1 WHERE id=$1"
			case "revision":
				query = "UPDATE event_team_labs SET desired_revision=2 WHERE id=$1"
			case "closed":
				query = "UPDATE event_team_labs SET desired_state='Stopped',logical_closed_at=now(),close_reason='solved' WHERE id=$1"
			}
			_, e := f.db.Pool.Exec(ctx, query, before.ID)
			require.NoError(t, e)
			_, e = f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, f.ownerID)
			require.Error(t, e)
			after, e := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
			require.NoError(t, e)
			require.Equal(t, before.ID, after.ID)
			require.Equal(t, before.Generation, after.Generation)
		})
	}
}

type lateBirthRetryQueries struct {
	use.IRepository
	gate bool
}

func (q *lateBirthRetryQueries) RetryNeverCreatedLabBindings(ctx context.Context, lab, team, event uuid.UUID, rev int64, op uuid.UUID) (int64, error) {
	// The exported use case must reject a stale CAS result and roll back every earlier reset.
	return 0, nil
}

type lateBirthRetryWorker struct {
	base postgres.IUnitOfWorker[use.IRepository]
}

func (w lateBirthRetryWorker) UnitOfWork(ctx context.Context) (context.Context, use.IRepository, postgres.UoW, error) {
	tx, q, u, e := w.base.UnitOfWork(ctx)
	if e != nil {
		return tx, q, u, e
	}
	return tx, &lateBirthRetryQueries{IRepository: q}, u, nil
}
func TestInitialFailedRetryCASRejectsBirthAfterProbe(t *testing.T) {
	f, set, before := prepareNeverCreatedFailure(t)
	worker := lateBirthRetryWorker{postgres.NewUnitOfWorker[use.IRepository](postgres.NewUoWFactory(f.db.Pool))}
	u := use.NewEventUseCase(use.Dependencies{Repo: f.db.Queries, UoW: worker, Infra: f.agent, InfrastructureCapability: standCapability{}})
	u.SetLifecycleControls(true)
	_, e := u.RecreateTeamStand(context.Background(), f.eventID, f.blueID, f.ownerID)
	require.Error(t, e)
	after, e := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	require.NoError(t, e)
	require.Equal(t, before.ID, after.ID)
	require.Empty(t, after.AgentUID)
	var n int
	require.NoError(t, f.db.Pool.QueryRow(context.Background(), "SELECT count(*) FROM lab_bindings WHERE event_team_id=$1 AND readiness=2", f.blueID).Scan(&n))
	require.Greater(t, n, 0)
}
