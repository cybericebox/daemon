package event_test

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestInitialProvisioningRequiresPersistedAdmission(t *testing.T) {
	f := newStandFixture(t)
	f.uc.SetAllocationAccounting(true)
	f.pass(t)
	require.Empty(t, f.agent.deployed, "missing event reservation must not reach create RPC")
}
func TestInitialProvisioningHoldsBeforeRPCAndNodeQueueIsNotReady(t *testing.T) {
	f := newStandFixture(t)
	f.uc.SetAllocationAccounting(true)
	ctx := context.Background()
	now := time.Now()
	r, err := calModel.NewEventReservation(calModel.EventInput{EventID: f.eventID, Window: calModel.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}, Teams: 20, PerTeam: calModel.Amount{CPUMillicores: 10000, MemoryBytes: 10 << 30}}, f.ownerID, now)
	require.NoError(t, err)
	r.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 20}}, 0, now)
	require.NoError(t, resourceCalendarRepo.New(f.db.Queries).CreateReservation(ctx, r))
	f.agent.beforeDeploy = func(ctx context.Context, group, lab string) error {
		var state string
		var cpu, memory int64
		err := f.db.Pool.QueryRow(ctx, `SELECT allocation->>'RuntimeState',(allocation->'AllocatedRequests'->>'CPUMillicores')::bigint,(allocation->'AllocatedRequests'->>'MemoryBytes')::bigint FROM event_team_labs WHERE lab_group_name=$1 AND lab_name=$2`, group, lab).Scan(&state, &cpu, &memory)
		require.NoError(t, err)
		require.Equal(t, "Admitted", state)
		require.Positive(t, cpu)
		require.Positive(t, memory)
		var groups int
		require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_team_group_allocations WHERE lab_group_name=$1`, group).Scan(&groups))
		require.Equal(t, 1, groups)
		return nil
	}
	f.pass(t)
	require.NotEmpty(t, f.agent.deployed)
	for key, meta := range f.agent.metas {
		require.NotNil(t, meta.InitialLifecycle, "managed create %s omitted persisted intent", key)
		var operation uuid.UUID
		var revision int64
		require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT operation_id,desired_revision FROM event_team_labs WHERE lab_group_name||'/'||lab_name=$1`, key).Scan(&operation, &revision))
		require.Equal(t, operation, meta.InitialLifecycle.OperationID)
		require.Equal(t, revision, meta.InitialLifecycle.Revision)
	}
	labs, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	require.NoError(t, err)
	require.NotEmpty(t, labs)
	for _, l := range labs {
		var a eventLabModel.Allocation
		require.NoError(t, json.Unmarshal(l.Allocation, &a))
		require.Equal(t, "Admitted", a.RuntimeState)
		require.False(t, l.RuntimeReady)
	}
	groups, err := f.db.Queries.ListEventGroupAllocations(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, groups, 3)
	for _, g := range groups {
		require.Positive(t, g.VpnMemoryBytes)
	}
	for _, lab := range f.agent.deployed {
		f.agent.queued[lab] = &exerciseModel.LabQueue{Reason: "InsufficientResources"}
	}
	f.pass(t)
	require.EqualValues(t, 0, f.readiness(t, f.blueID, f.infraOne), "budget alone must never grant Ready/publication")

}

func (a *standAgent) GroupSizes(plan infraModel.GroupPlan) (infraModel.GroupSizes, bool) {
	return infraModel.GroupSizes{VPN: calModel.Amount{CPUMillicores: 250, MemoryBytes: 256 << 20}, Gateway: calModel.Amount{CPUMillicores: 125, MemoryBytes: 128 << 20}}, true
}
func (a *standAgent) NeedFit(infraModel.PlacementNeed) *infraModel.FitViolation { return nil }

func allocationFixture(t *testing.T, quota int64) (*standFixture, []uuid.UUID) {
	t.Helper()
	f := newStandFixture(t)
	f.attachSharedSet(t)
	f.uc.SetAllocationAccounting(true)
	f.pass(t)
	ctx := context.Background()
	now := time.Now()
	r, err := calModel.NewEventReservation(calModel.EventInput{EventID: f.eventID, Window: calModel.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}, Teams: 1, PerTeam: calModel.Amount{CPUMillicores: 1125, MemoryBytes: 896 << 20}, PerTeamSnapshotQuotaBytes: quota}, f.ownerID, now)
	require.NoError(t, err)
	r.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 1}}, 0, now)
	require.NoError(t, resourceCalendarRepo.New(f.db.Queries).CreateReservation(ctx, r))
	rows, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	require.NoError(t, err)
	ids := []uuid.UUID{}
	for _, row := range rows {
		if row.EventTeamID == f.blueID {
			ids = append(ids, row.ID)
		}
	}
	require.Len(t, ids, 2)
	return f, ids
}
func TestConcurrentEventAdmissionsCannotDoubleSpendAndRetainUnknown(t *testing.T) {
	f, ids := allocationFixture(t, 1<<30)
	ctx := context.Background()
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, id := range ids {
		go func(id uuid.UUID) {
			<-start
			errs <- f.uc.AdmitEventLabStart(ctx, f.eventID, f.blueID, id, eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}, 1<<30)
		}(id)
	}
	close(start)
	success := 0
	for range 2 {
		if <-errs == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	compute, err := f.uc.HeldCompute(ctx, f.eventID)
	require.NoError(t, err)
	require.Equal(t, eventLabModel.Compute{CPUMillicores: 1125, MemoryBytes: 896 << 20}, compute)
	storage, err := f.uc.HeldStorage(ctx, f.eventID)
	require.NoError(t, err)
	require.EqualValues(t, 1<<30, storage.SnapshotQuotaBytes)
	require.False(t, storage.PhysicalKnown)
	plan, err := f.uc.GetResourcePlan(ctx, f.eventID)
	require.NoError(t, err)
	require.False(t, plan.Observation.Complete)
	require.Equal(t, "1125", plan.Observation.Held.CPUMillicores)
	require.Equal(t, "750", plan.Observation.PendingStarts.CPUMillicores)
	require.Equal(t, "375", plan.Observation.GroupServices.CPUMillicores)
	groups, err := f.db.Queries.ListEventGroupAllocations(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, groups, 1)
}
func TestPersistentAdmissionRequiresConfiguredLogicalQuota(t *testing.T) {
	f, ids := allocationFixture(t, 0)
	ctx := context.Background()
	require.Error(t, f.uc.AdmitEventLabStart(ctx, f.eventID, f.blueID, ids[0], eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}, 1<<30))
	compute, err := f.uc.HeldCompute(ctx, f.eventID)
	require.NoError(t, err)
	require.Equal(t, eventLabModel.Compute{}, compute)
	groups, err := f.db.Queries.ListEventGroupAllocations(ctx, f.eventID)
	require.NoError(t, err)
	require.Empty(t, groups)
}

// These scenarios are written for the final integrated run; this checkpoint
// deliberately does not execute them under the owner's changed workflow.
type staleAllocationQueries struct {
	*postgres.Queries
	old postgres.EventTeamLab
}

func (q staleAllocationQueries) GetEventTeamLab(ctx context.Context, id uuid.UUID) (postgres.EventTeamLab, error) {
	if id == q.old.ID {
		return q.old, nil
	}
	return q.Queries.GetEventTeamLab(ctx, id)
}
func TestObservationCannotOverwriteAllocationAtSameTimestamp(t *testing.T) {
	f, ids := allocationFixture(t, 0)
	ctx := context.Background()
	repo := eventLabRepo.New(f.db.Queries)
	lab, err := repo.Get(ctx, ids[0])
	require.NoError(t, err)
	ok, err := repo.RecordInitialIdentity(ctx, lab.ID, lab.Ref, "uid", 1, lab.UpdatedAt)
	require.NoError(t, err)
	require.True(t, ok)
	old, err := f.db.Queries.GetEventTeamLab(ctx, lab.ID)
	require.NoError(t, err)
	current, err := eventLabRepo.ToDomain(old)
	require.NoError(t, err)
	require.True(t, current.Admit(eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}, 0, old.UpdatedAt))
	ok, err = repo.Update(ctx, current, current.Revision)
	require.NoError(t, err)
	require.True(t, ok)
	at := old.UpdatedAt.Add(time.Second)
	o := eventLabModel.Observation{Ref: current.Ref, UID: "uid", OperationID: current.OperationID, Generation: 1, ObservedGeneration: 1, Revision: 1, DesiredState: "Running", ActualState: "Starting", ObservedAt: &at, Allocation: eventLabModel.Allocation{RuntimeState: "Allocated"}}
	// Force the earlier reader's row while the real DB already holds admission.
	// Timestamp/revision/previous observation are deliberately equal; allocation
	// CAS is the only changed predicate and must reject the stale normalized JSON.
	accepted, err := eventLabRepo.New(staleAllocationQueries{Queries: f.db.Queries, old: old}).RecordObservation(ctx, lab.ID, o)
	require.NoError(t, err)
	require.False(t, accepted)
	got, err := repo.Get(ctx, lab.ID)
	require.NoError(t, err)
	require.Equal(t, eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}, got.HeldCompute())
	require.Equal(t, "Admitted", got.Allocation.RuntimeState)
}

// Synthetic exact stopped receipts exercise durable budget accounting only.
// This fixture does not claim that native child/group stop occurred.
func TestReleasedOtherGroupDoesNotConsumeNewTeamAdmission(t *testing.T) {
	f, ids := allocationFixture(t, 0)
	ctx := context.Background()
	need := eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}
	require.NoError(t, f.uc.AdmitEventLabStart(ctx, f.eventID, f.blueID, ids[0], need, 0))
	at := time.Now().UTC()
	labs := eventLabRepo.New(f.db.Queries)
	require.NotEmpty(t, ids)
	ok, err := labs.RecordInitialIdentity(ctx, ids[0], mustAllocationLab(t, f, ids[0]).Ref, "synthetic-child", 1, at)
	require.NoError(t, err)
	require.True(t, ok)
	lab := mustAllocationLab(t, f, ids[0])
	before := lab.Revision
	require.NoError(t, lab.Close("manual", uuid.Must(uuid.NewV7()), at))
	ok, err = labs.Update(ctx, lab, before)
	require.NoError(t, err)
	require.True(t, ok)
	observed := at.Add(time.Second)
	ok, err = labs.RecordObservation(ctx, lab.ID, eventLabModel.Observation{Ref: lab.Ref, UID: lab.AgentUID, Generation: 1, ObservedGeneration: 1, OperationID: lab.OperationID, Revision: lab.Revision, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: "NotRequired", AccessFenced: true, AccessFencedAt: &observed, AccessFenceVPNBootID: "synthetic-boot", StoppedAt: &observed, ObservedAt: &observed, Allocation: eventLabModel.Allocation{RuntimeState: "Released", ReleasedAt: &observed, StorageState: "None"}})
	require.NoError(t, err)
	require.True(t, ok)
	// Persist a current fenced receipt for the old group's services; quota and
	// immutable pod sizes remain separate from current held compute.
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_group_allocations SET agent_uid='synthetic-group',agent_generation=1,desired_state='Stopped',actual_state='Stopped',observed_revision=desired_revision,observed_at=$2,access_fenced=true,allocation=jsonb_set(jsonb_set(allocation,'{RuntimeState}','"Released"'),'{ReleasedAt}',to_jsonb($2::timestamptz)) WHERE event_team_id=$1`, f.blueID, observed)
	require.NoError(t, err)
	held, err := f.uc.HeldCompute(ctx, f.eventID)
	require.NoError(t, err)
	require.Equal(t, eventLabModel.Compute{}, held)
	rows, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	require.NoError(t, err)
	var next uuid.UUID
	for _, row := range rows {
		if row.EventTeamID == f.redID {
			next = row.ID
			break
		}
	}
	require.NotEqual(t, uuid.Nil, next)
	require.NoError(t, f.uc.AdmitEventLabStart(ctx, f.eventID, f.redID, next, need, 0))
	held, err = f.uc.HeldCompute(ctx, f.eventID)
	require.NoError(t, err)
	require.Equal(t, eventLabModel.Compute{CPUMillicores: 1125, MemoryBytes: 896 << 20}, held)
}
func mustAllocationLab(t *testing.T, f *standFixture, id uuid.UUID) eventLabModel.Lab {
	t.Helper()
	l, e := eventLabRepo.New(f.db.Queries).Get(context.Background(), id)
	require.NoError(t, e)
	return l
}

func TestManagedInitialReadinessAdoptsUIDButRequiresExactRunningCertificate(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	f.uc.SetAllocationAccounting(true)
	reservation, err := calModel.NewEventReservation(calModel.EventInput{EventID: f.eventID, Window: calModel.Window{Start: now.Add(-time.Hour), End: now.Add(time.Hour)}, Teams: 20, PerTeam: calModel.Amount{CPUMillicores: 10000, MemoryBytes: 10 << 30}}, f.ownerID, now)
	require.NoError(t, err)
	reservation.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 20}}, 0, now)
	require.NoError(t, resourceCalendarRepo.New(f.db.Queries).CreateReservation(ctx, reservation))
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.uc.SetLifecycleControls(true)
	f.pass(t)
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, f.infraOne)
	require.NoError(t, err)
	require.NotEmpty(t, lab.AgentUID, "identity must be adopted independently of ACL publication")
	require.False(t, lab.RuntimeReady, "generic Ready cannot certify current initial operation")
	at := time.Now().UTC()
	f.agent.mu.Lock()
	if f.agent.observations == nil {
		f.agent.observations = map[eventLabModel.Ref]eventLabModel.Observation{}
	}
	f.agent.observations[lab.Ref] = eventLabModel.Observation{Ref: lab.Ref, UID: lab.AgentUID, Generation: lab.AgentGeneration, ObservedGeneration: lab.AgentGeneration, OperationID: lab.OperationID, Revision: lab.Revision, DesiredState: "Running", ActualState: "Running", RuntimeReady: true, ObservedAt: &at, Allocation: eventLabModel.Allocation{RuntimeState: "Allocated", AllocatedRequests: lab.HeldCompute(), ObservedAt: &at, StorageState: "None"}}
	f.agent.mu.Unlock()
	f.pass(t)
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
	require.NoError(t, err)
	require.True(t, current.RuntimeReady)
	require.Equal(t, current.Revision, current.ObservedRevision)
	require.EqualValues(t, 0, f.readiness(t, f.blueID, f.infraOne), "missing current ACL certificate must still prevent publication")
}
