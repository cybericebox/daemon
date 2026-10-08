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
