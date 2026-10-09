package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabGroupRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/stretchr/testify/require"
)

type groupRecoveryQueries struct{ event.IRepository }

func (q groupRecoveryQueries) ListDirtyEventTeamLabs(context.Context, postgres.ListDirtyEventTeamLabsParams) ([]postgres.EventTeamLab, error) {
	return nil, nil // isolate group coordination from unrelated Lab workers
}

type groupRecoveryAgent struct {
	*standAgent
	observation eventLabModel.GroupObservation
	stops       []eventLabModel.GroupTarget
}

func (a *groupRecoveryAgent) ObserveLabGroup(_ context.Context, name string) (eventLabModel.GroupObservation, error) {
	if name == a.observation.Name {
		return a.observation, nil
	}
	return eventLabModel.GroupObservation{Name: name}, nil
}
func (a *groupRecoveryAgent) StopLabGroup(_ context.Context, target eventLabModel.GroupTarget) error {
	a.stops = append(a.stops, target)
	return nil
}
func (a *groupRecoveryAgent) StartLabGroup(context.Context, eventLabModel.GroupTarget) error {
	return nil
}

// Actual DB/UOW/receipt-adoption regression. Native observations are explicit
// adapters; this proves safe command intent, never physical native release.
func groupRecoveryFixture(t *testing.T) (*standFixture, *groupRecoveryAgent, eventLabModel.Group) {
	t.Helper()
	f, ids := allocationFixture(t, 0)
	ctx := context.Background()
	rows, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	require.NoError(t, err)
	var groupName string
	for _, row := range rows {
		if row.EventTeamID == f.blueID {
			groupName = row.LabGroupName
		}
	}
	need := infraModel.GroupSizes{VPN: calModel.Amount{CPUMillicores: 125, MemoryBytes: 512 << 20}, Gateway: calModel.Amount{CPUMillicores: 15, MemoryBytes: 64 << 20}}
	require.NoError(t, eventLabAllocationRepo.New(f.db.Queries).CreateGroup(ctx, eventLabAllocationRepo.Group{TeamID: f.blueID, EventID: f.eventID, Name: groupName, Sizes: need, CreatedAt: time.Now().UTC()}))
	repo := eventLabRepo.New(f.db.Queries)
	for _, id := range ids {
		l, err := repo.Get(ctx, id)
		require.NoError(t, err)
		at := time.Now().UTC()
		require.True(t, l.Admit(eventLabModel.Compute{CPUMillicores: 31, MemoryBytes: 128 << 20}, 0, at))
		require.True(t, l.RecordCreateDispatch("original-group-uid", "wire-"+id.String(), l.OperationID, at))
		ok, err := repo.Update(ctx, l, l.Revision)
		require.NoError(t, err)
		require.True(t, ok)
		receipt := &eventLabModel.CreationReceipt{GroupUID: "original-group-uid", NamespaceUID: "original-namespace-uid", DefinitionHash: l.CreateEvidence.DefinitionHash, CreationID: "birth-" + id.String(), LabUID: "lab-" + id.String(), OperationID: l.CreateEvidence.OperationID, Revision: 1, Committed: true}
		ok, err = repo.RecordBirthIdentity(ctx, id, eventLabModel.Observation{Ref: l.Ref, UID: receipt.LabUID, Generation: 1, Creation: receipt}, at)
		require.NoError(t, err)
		require.True(t, ok)
		waveStopped(t, f, id, "solved")
	}
	f.shiftLifecycle(t, -2*time.Hour, -time.Hour)
	g, err := eventLabGroupRepo.New(f.db.Queries).Get(ctx, f.blueID)
	require.NoError(t, err)
	at := time.Now().UTC()
	a := &groupRecoveryAgent{standAgent: f.agent, observation: eventLabModel.GroupObservation{Name: groupName, UID: "original-group-uid", Generation: 1, ObservedAt: &at, DesiredState: "Running", InitialReady: true, Ready: true, ImmutableSizesKnown: true, VPNSize: eventLabModel.Compute{CPUMillicores: 125, MemoryBytes: 320 << 20}, GatewaySize: eventLabModel.Compute{CPUMillicores: 15, MemoryBytes: 32 << 20}}}
	uc := event.NewEventUseCase(event.Dependencies{Repo: groupRecoveryQueries{f.db.Queries}, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: a, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets}})
	uc.SetLifecycleControls(true)
	f.uc = uc
	return f, a, g
}

func TestGroupRecoveryAdoptedBirthAnchorsUndersizedOriginalStopWithoutCredit(t *testing.T) {
	f, a, before := groupRecoveryFixture(t)
	require.Empty(t, before.AgentUID)
	oldHeld := before.HeldCompute()
	require.NoError(t, f.uc.ReconcilePendingLabLifecycles(context.Background()))
	g, err := eventLabGroupRepo.New(f.db.Queries).Get(context.Background(), f.blueID)
	require.NoError(t, err)
	require.Equal(t, "original-group-uid", g.AgentUID, "size mismatch must not prevent receipt-bound original identity recovery")
	require.Equal(t, "Stopped", g.DesiredState)
	require.False(t, g.Ready)
	require.Equal(t, oldHeld, g.HeldCompute())
	require.Equal(t, before.ConfiguredRequests, g.ConfiguredRequests)
	require.Equal(t, before.Allocation, g.Allocation)
	require.Len(t, a.stops, 1)
	require.Equal(t, g.Name, a.stops[0].Group)
	require.Equal(t, "original-group-uid", a.stops[0].ExpectedUID)
	require.Equal(t, g.OperationID, a.stops[0].OperationID)
	require.Equal(t, g.Revision, a.stops[0].Revision)
}

func TestGroupRecoveryRejectsForeignUnadoptedAndStaleProof(t *testing.T) {
	for _, kind := range []string{"replacement-group", "wrong-name", "wrong-birth-ref", "unadopted", "stale-generation", "conflicting-namespace", "missing-evidence", "wrong-pinned-hash", "stale-observation"} {
		t.Run(kind, func(t *testing.T) {
			f, a, before := groupRecoveryFixture(t)
			ctx := context.Background()
			switch kind {
			case "replacement-group":
				a.observation.UID = "replacement-group-uid"
			case "wrong-name":
				a.observation.Name = "other-group"
			case "stale-observation":
				at := time.Now().UTC().Add(-time.Minute)
				a.observation.ObservedAt = &at
			default:
				children, err := eventLabGroupRepo.New(f.db.Queries).LockChildren(ctx, f.blueID)
				require.NoError(t, err)
				require.NotEmpty(t, children)
				l := children[0]
				if kind == "wrong-pinned-hash" {
					_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET definition_hash=repeat('0',64) WHERE id=$1`, l.ID)
					require.NoError(t, err)
				} else {
					switch kind {
					case "wrong-birth-ref":
						copy := *l.CreateEvidence
						copy.Ref.Lab = "foreign-lab"
						l.CreateEvidence = &copy
					case "unadopted":
						copy := *l.CreateEvidence
						copy.CreationID = ""
						l.CreateEvidence = &copy
					case "stale-generation":
						copy := *l.CreateEvidence
						copy.DeploymentGeneration++
						l.CreateEvidence = &copy
					case "conflicting-namespace":
						copy := *l.CreateEvidence
						copy.NamespaceUID = "different-namespace"
						l.CreateEvidence = &copy
					case "missing-evidence":
						l.CreateEvidence = nil
					}
					ok, err := eventLabRepo.New(f.db.Queries).Update(ctx, l, l.Revision)
					require.NoError(t, err)
					require.True(t, ok)
				}
			}
			require.NoError(t, f.uc.ReconcilePendingLabLifecycles(ctx))
			after, err := eventLabGroupRepo.New(f.db.Queries).Get(ctx, f.blueID)
			require.NoError(t, err)
			require.Empty(t, after.AgentUID)
			require.False(t, after.Ready)
			require.Equal(t, before.HeldCompute(), after.HeldCompute())
			require.Equal(t, before.ConfiguredRequests, after.ConfiguredRequests)
			require.Equal(t, before.Allocation, after.Allocation)
			require.Empty(t, a.stops)
		})
	}
}
