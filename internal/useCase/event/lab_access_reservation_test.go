package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

type labAccessReservationGate struct {
	fakeResourceGate
	reserved bool
	startsAt time.Time
}

func (g *labAccessReservationGate) EventReservationStart(context.Context, uuid.UUID) (time.Time, bool, error) {
	return g.startsAt, g.reserved, nil
}

func (g *labAccessReservationGate) HasEventReservation(context.Context, uuid.UUID) (bool, error) {
	return g.reserved, nil
}

type labAccessReservationInfra struct {
	groupPresenceInfra
	fit *infraModel.FitViolation
}

func (i *labAccessReservationInfra) GroupSizes(infraModel.GroupPlan) (infraModel.GroupSizes, bool) {
	return infraModel.GroupSizes{
		VPN:     resourcesModel.Amount{CPUMillicores: 125, MemoryBytes: 512 << 20},
		Gateway: resourcesModel.Amount{CPUMillicores: 15, MemoryBytes: 64 << 20},
	}, true
}

func (i *labAccessReservationInfra) NeedFit(infraModel.PlacementNeed) *infraModel.FitViolation {
	return i.fit
}

type labAccessReservationRevisionInfra struct {
	*labAccessReservationInfra
}

func (i *labAccessReservationRevisionInfra) ReconcileLabGroupAccessRevision(_ context.Context, _ string, policies []labAccessModel.ClientPolicy, _ eventLabModel.AccessTarget) error {
	i.operations = append(i.operations, "policy")
	i.policies = policies
	return nil
}

type labAccessReservationFixture struct {
	uc              *event.EventUseCase
	q               *postgresMocks.MockQuerier
	infra           *labAccessReservationInfra
	gate            *labAccessReservationGate
	unit            *testUoW
	eventID, teamID uuid.UUID
	budget          postgres.ResourceReservation
	created         []postgres.CreateEventGroupAllocationParams
}

// The event has no lab devices, but its VPN and gateway still have real demand.
// Planning, reservation waiting and ledger admission use their production paths.
func newLabAccessReservationFixture(t *testing.T, accounting, exists bool) *labAccessReservationFixture {
	t.Helper()
	q := newFormGateMock(gomock.NewController(t))
	f := &labAccessReservationFixture{
		q: q, infra: &labAccessReservationInfra{groupPresenceInfra: groupPresenceInfra{&recordingLabAccessInfra{}, exists}},
		gate: &labAccessReservationGate{}, unit: &testUoW{}, eventID: uuid.Must(uuid.NewV7()), teamID: uuid.Must(uuid.NewV7()),
	}
	now := time.Now().UTC()
	f.budget = postgres.ResourceReservation{
		ID: uuid.Must(uuid.NewV7()), Kind: "event", EventID: uuid.NullUUID{UUID: f.eventID, Valid: true},
		StartsAt: now.Add(-time.Hour), EndsAt: now.Add(time.Hour), Teams: 1,
		PerTeamCpuMillicores: 140, PerTeamMemoryBytes: 576 << 20, SizeCpuMillicores: 140, SizeMemoryBytes: 576 << 20,
		Placement: []byte(`[{"agent_id":"` + uuid.Must(uuid.NewV7()).String() + `","units":1}]`),
		CreatedAt: now, UpdatedAt: now,
	}
	f.gate.startsAt = f.budget.StartsAt
	f.uc = event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: f.unit}, Infra: f.infra})
	f.uc.SetResourceGate(f.gate)
	f.uc.SetAllocationAccounting(accounting)
	q.EXPECT().GetEventConfig(gomock.Any(), f.eventID).Return(postgres.EventConfig{EventID: f.eventID}, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), f.eventID).Return(startedEvent(f.eventID, now), nil).AnyTimes()
	q.EXPECT().ListEventExercises(gomock.Any(), f.eventID).Return(nil, nil).AnyTimes()
	q.EXPECT().ListEventExerciseDetails(gomock.Any(), f.eventID).Return(nil, nil).AnyTimes()
	q.EXPECT().LockResourceCalendar(gomock.Any()).Return(nil).AnyTimes()
	q.EXPECT().LockEventConfigForLabSizing(gomock.Any(), f.eventID).Return(f.eventID, nil).AnyTimes()
	q.EXPECT().ListUnaccountedEventLabStarts(gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().ListEventLabAllocations(gomock.Any(), f.eventID).Return(nil, nil).AnyTimes()
	q.EXPECT().ListEventGroupAllocations(gomock.Any(), f.eventID).Return(nil, nil).AnyTimes()
	q.EXPECT().GetEventResourceReservation(gomock.Any(), uuid.NullUUID{UUID: f.eventID, Valid: true}).DoAndReturn(
		func(context.Context, uuid.NullUUID) (postgres.ResourceReservation, error) {
			if !f.gate.reserved {
				return postgres.ResourceReservation{}, pgx.ErrNoRows
			}
			return f.budget, nil
		}).AnyTimes()
	q.EXPECT().CreateEventGroupAllocation(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, in postgres.CreateEventGroupAllocationParams) error {
			f.created = append(f.created, in)
			return nil
		}).AnyTimes()
	return f
}

func (f *labAccessReservationFixture) expectSync(vpn, runtime bool, passes int) {
	f.q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{
		{EventTeamID: f.teamID, EventID: f.eventID, DesiredRevision: 2, AppliedRevision: 1, VpnEnabled: vpn, RuntimeOpen: runtime},
	}, nil).Times(passes)
	// No acknowledgement is expected: waiting must preserve the dirty revision.
	f.q.EXPECT().ListEventLabAccessClients(gomock.Any(), f.teamID).Return([]uuid.UUID{uuid.Must(uuid.NewV7())}, nil).AnyTimes()
	f.q.EXPECT().ListEventLabAccessLabs(gomock.Any(), f.teamID).Return(nil, nil).AnyTimes()
}

func TestReconcilePendingLabAccess_NoLabsWaitsForGroupReservation(t *testing.T) {
	f := newLabAccessReservationFixture(t, true, false)
	f.expectSync(true, true, 3)
	for range 2 {
		require.NoError(t, f.uc.ReconcilePendingLabAccess(context.Background()), "a missing reservation is a wait, not a fault")
	}
	require.Empty(t, f.infra.operations, "an unreserved group must never be created or resumed")
	require.Empty(t, f.created)
	require.False(t, f.unit.saved)

	f.gate.reserved = true
	require.NoError(t, f.uc.ReconcilePendingLabAccess(context.Background()), "the dirty sync must retry as soon as the reservation appears")
	require.Equal(t, []string{"ensure", "policy", "resume", "group-resume"}, f.infra.operations)
	require.Len(t, f.created, 1)
	require.Equal(t, f.teamID, f.created[0].EventTeamID)
	require.EqualValues(t, 125, f.created[0].VpnCpuMillicores)
	require.EqualValues(t, 15, f.created[0].GatewayCpuMillicores)
	require.True(t, f.unit.saved, "the group envelope is admitted before the create RPC")
}

func TestReconcilePendingLabAccess_NoLabsKeepsLegacyWithoutReservation(t *testing.T) {
	f := newLabAccessReservationFixture(t, false, false)
	f.expectSync(true, true, 1)
	require.NoError(t, f.uc.ReconcilePendingLabAccess(context.Background()))
	require.Equal(t, []string{"ensure", "policy", "resume", "group-resume"}, f.infra.operations)
	require.Empty(t, f.created)
}

func TestReconcilePendingLabAccess_MissingReservationStillRevokesExistingGroup(t *testing.T) {
	for _, vpn := range []bool{true, false} {
		t.Run(map[bool]string{true: "revoke", false: "stop"}[vpn], func(t *testing.T) {
			f := newLabAccessReservationFixture(t, true, true)
			f.q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{
				{EventTeamID: f.teamID, EventID: f.eventID, DesiredRevision: 3, AppliedRevision: 2, VpnEnabled: vpn, RuntimeOpen: false},
			}, nil)
			f.q.EXPECT().ListEventLabAccessClients(gomock.Any(), f.teamID).Return([]uuid.UUID{uuid.Must(uuid.NewV7())}, nil)
			f.q.EXPECT().ListEventLabAccessLabs(gomock.Any(), f.teamID).Return([]postgres.ListEventLabAccessLabsRow{
				{LabGroupName: testLabGroup(f.eventID, f.teamID), LabName: "c-existing", Available: true},
			}, nil)
			require.NoError(t, f.uc.ReconcilePendingLabAccess(context.Background()))
			want := []string{"policy"}
			if !vpn {
				want = []string{"suspend", "group-suspend", "policy"}
			}
			require.Equal(t, want, f.infra.operations)
			require.Len(t, f.infra.policies, 1)
			require.Empty(t, f.infra.policies[0].AllowedLabs, "runtime closure must revoke the existing lab")
			require.Empty(t, f.created)
			require.False(t, f.unit.saved)
		})
	}
}

func TestReconcilePendingLabAccess_ReservationWaitKeepsRevisionDirtyAfterRevocation(t *testing.T) {
	f := newLabAccessReservationFixture(t, true, true)
	agent := &labAccessReservationRevisionInfra{f.infra}
	f.uc = event.NewEventUseCase(event.Dependencies{Repo: f.q, UoW: testUnitOfWorker{repo: f.q, unit: f.unit}, Infra: agent})
	f.uc.SetResourceGate(f.gate)
	f.uc.SetAllocationAccounting(true)
	group := testLabGroup(f.eventID, f.teamID)
	var operation uuid.UUID
	var fingerprint string
	var applied int64
	f.q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).DoAndReturn(
		func(context.Context, int32) ([]postgres.ListDirtyEventLabAccessSyncsRow, error) {
			if applied == 2 {
				return nil, nil
			}
			return []postgres.ListDirtyEventLabAccessSyncsRow{{
				EventTeamID: f.teamID, EventID: f.eventID, DesiredRevision: 2, AppliedRevision: applied, VpnEnabled: true,
				OperationID: uuid.NullUUID{UUID: operation, Valid: operation != uuid.Nil}, PolicyFingerprint: fingerprint, ExpectedGroupUid: "group-uid",
			}}, nil
		}).Times(2)
	f.q.EXPECT().ListEventLabAccessClients(gomock.Any(), f.teamID).Return([]uuid.UUID{uuid.Must(uuid.NewV7())}, nil).Times(2)
	f.q.EXPECT().ListEventLabAccessLabs(gomock.Any(), f.teamID).Return([]postgres.ListEventLabAccessLabsRow{
		{LabGroupName: group, LabName: "c-existing", Available: true},
	}, nil).Times(2)
	f.q.EXPECT().MaterializeEventLabAccessPolicy(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p postgres.MaterializeEventLabAccessPolicyParams) (postgres.EventLabAccessSync, error) {
			operation, fingerprint = p.OperationID.UUID, p.PolicyFingerprint
			return postgres.EventLabAccessSync{OperationID: p.OperationID, DesiredRevision: 2}, nil
		}).AnyTimes()
	f.q.EXPECT().GetCurrentEventLabAccessMonitoring(gomock.Any(), f.teamID).DoAndReturn(
		func(context.Context, uuid.UUID) (postgres.LabMonitoringCurrent, error) {
			now := time.Now().UTC()
			// A fresh matching ACL certificate is deliberately present. It proves
			// revocation, but cannot prove the deferred group admission/start.
			payload, err := json.Marshal(map[string]any{
				"groups": []any{map[string]any{"name": group, "uid": "group-uid", "status": map[string]any{
					"currentVpnBootId": "boot", "currentVpnBootAvailable": true, "currentVpnBootObservedUnixMs": now.UnixMilli(),
				}}},
				"policies": []any{map[string]any{
					"labGroupName": group, "expectedGroupUid": "group-uid", "policyUid": "policy-uid", "generation": "1",
					"operationId": operation.String(), "desiredRevision": "2", "status": map[string]any{
						"operationId": operation.String(), "appliedRevision": "2", "observedGeneration": "1",
						"state": "Applied", "vpnBootId": "boot", "appliedAtUnixMs": now.UnixMilli(),
					},
				}},
			})
			return postgres.LabMonitoringCurrent{LabGroupName: group, ObservedAt: now, Payload: payload}, err
		}).AnyTimes()
	f.q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, p postgres.MarkEventLabAccessSyncAppliedParams) (int64, error) {
			applied = p.DesiredRevision
			return 1, nil
		}).AnyTimes()

	require.NoError(t, f.uc.ReconcilePendingLabAccess(context.Background()))
	require.Equal(t, []string{"policy"}, f.infra.operations)
	require.Empty(t, f.infra.policies[0].AllowedLabs, "the revocation is still dispatched while waiting")
	require.Zero(t, applied, "ACL proof must not acknowledge deferred admission/start")
	require.Empty(t, f.created)

	f.gate.reserved = true
	require.NoError(t, f.uc.ReconcilePendingLabAccess(context.Background()))
	require.Equal(t, []string{"policy", "ensure", "policy", "resume", "group-resume"}, f.infra.operations)
	require.EqualValues(t, 2, applied, "reservation arrival retries admission/start and permits acknowledgement")
	require.Len(t, f.created, 1)
}

func TestReconcilePendingLabAccess_ExistingReservationStillRejectsInvalidAdmission(t *testing.T) {
	for _, invalid := range []string{"budget", "window", "placement", "fit"} {
		t.Run(invalid, func(t *testing.T) {
			f := newLabAccessReservationFixture(t, true, false)
			f.gate.reserved = true
			f.expectSync(true, true, 1)
			switch invalid {
			case "budget":
				f.budget.SizeCpuMillicores = 1
			case "window":
				f.budget.EndsAt = time.Now().UTC().Add(-time.Minute)
			case "placement":
				f.budget.Unplaced = 1
			case "fit":
				f.infra.fit = &infraModel.FitViolation{Resource: "vpn_users", Requested: 2, Max: 1}
			}
			require.ErrorIs(t, f.uc.ReconcilePendingLabAccess(context.Background()), calModel.ErrNotEnoughReserved.Err())
			require.Empty(t, f.infra.operations)
			require.Empty(t, f.created)
			require.False(t, f.unit.saved)
		})
	}
}

// Existing admission must also reject finite reservations outside their slot;
// early moderators-team initialization cannot create services on a future hold.
func TestReconcilePendingLabAccess_NoLabsWaitsForReservationWindow(t *testing.T) {
	for _, kind := range []string{"before reservation", "after reservation", "unbounded reservation"} {
		t.Run(kind, func(t *testing.T) {
			f := newLabAccessReservationFixture(t, true, false)
			f.gate.reserved = true
			switch kind {
			case "before reservation":
				f.budget.StartsAt = time.Now().Add(time.Hour)
				f.budget.EndsAt = time.Now().Add(2 * time.Hour)
			case "after reservation":
				f.budget.EndsAt = time.Now().Add(-time.Second)
			case "unbounded reservation":
				f.budget.EndsAt = time.Time{}
			}
			f.expectSync(true, false, 1)
			require.Error(t, f.uc.ReconcilePendingLabAccess(context.Background()))
			require.Empty(t, f.infra.operations)
			require.Empty(t, f.created)
			require.False(t, f.unit.saved)
		})
	}
}

type eventPreviewHeldRepo struct {
	event.IRepository
	rows []postgres.ResourceTestLabHold
}

func (r eventPreviewHeldRepo) ListEventPreviewResourceHolds(context.Context, uuid.NullUUID) ([]postgres.ResourceTestLabHold, error) {
	return r.rows, nil
}

func TestEventGroupAdmissionCountsAlreadyAdmittedParticipantPreviews(t *testing.T) {
	f := newLabAccessReservationFixture(t, true, false)
	f.gate.reserved = true
	f.expectSync(true, true, 1)
	repo := eventPreviewHeldRepo{IRepository: f.q, rows: []postgres.ResourceTestLabHold{{EventID: uuid.NullUUID{UUID: f.eventID, Valid: true}, CpuMillicores: 1, MemoryBytes: 1}}}
	f.uc = event.NewEventUseCase(event.Dependencies{Repo: repo, UoW: testUnitOfWorker{repo: repo, unit: f.unit}, Infra: f.infra})
	f.uc.SetResourceGate(f.gate)
	f.uc.SetAllocationAccounting(true)
	err := f.uc.ReconcilePendingLabAccess(context.Background())
	require.Error(t, err, "the preview and a full team envelope cannot both spend the same reservation")
	require.Empty(t, f.created)
	require.Empty(t, f.infra.operations)
}

func TestReconcilePendingLabAccess_ReservationWindowInTheFutureIsAWait(t *testing.T) {
	f := newLabAccessReservationFixture(t, true, false)
	f.gate.reserved = true
	f.budget.StartsAt, f.budget.EndsAt = time.Now().UTC().Add(time.Hour), time.Now().UTC().Add(2*time.Hour)
	f.gate.startsAt = f.budget.StartsAt
	f.expectSync(true, true, 2)
	for range 2 {
		require.NoError(t, f.uc.ReconcilePendingLabAccess(context.Background()), "a window that has not started is a wait, with no journal entry")
	}
	require.Empty(t, f.infra.operations)
	require.Empty(t, f.created)

	// The window opens: the same sync is admitted.
	f.budget.StartsAt = time.Now().UTC().Add(-time.Minute)
	f.gate.startsAt = f.budget.StartsAt
	f.q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{
		{EventTeamID: f.teamID, EventID: f.eventID, DesiredRevision: 2, AppliedRevision: 1, VpnEnabled: true, RuntimeOpen: true},
	}, nil)
	require.NoError(t, f.uc.ReconcilePendingLabAccess(context.Background()))
	require.Len(t, f.created, 1)
}
