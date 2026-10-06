package event_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type recordingLabAccessInfra struct {
	policies   []labAccessModel.ClientPolicy
	operations []string
	group      string
	err        error
	suspendErr error
}

func (i *recordingLabAccessInfra) DeployLab(context.Context, string, string, infraModel.LabMeta, exerciseModel.Topology) error {
	return nil
}
func (i *recordingLabAccessInfra) LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{}, nil
}
func (i *recordingLabAccessInfra) EnsureLabClient(context.Context, string, string) (string, error) {
	return "", nil
}
func (i *recordingLabAccessInfra) EnsureVPNGroup(_ context.Context, group string) error {
	i.group = group
	i.operations = append(i.operations, "ensure")
	return nil
}
func (i *recordingLabAccessInfra) DestroyLabGroup(context.Context, string) error { return nil }
func (i *recordingLabAccessInfra) SetLabGroupVPNDisabled(_ context.Context, _ string, disabled bool) error {
	if disabled {
		i.operations = append(i.operations, "suspend")
	} else {
		i.operations = append(i.operations, "resume")
	}
	return i.suspendErr
}
func (i *recordingLabAccessInfra) SetLabGroupSuspended(_ context.Context, _ string, suspended bool) error {
	if suspended {
		i.operations = append(i.operations, "group-suspend")
	} else {
		i.operations = append(i.operations, "group-resume")
	}
	return nil
}
func (i *recordingLabAccessInfra) ReconcileLabGroupAccess(_ context.Context, _ string, policies []labAccessModel.ClientPolicy) error {
	i.operations = append(i.operations, "policy")
	i.policies = policies
	return i.err
}

func TestReconcilePendingLabAccess_UsesOnlyAvailableLabs(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID, firstUser, secondUser := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, AppliedRevision: 1, UpdatedAt: now, RuntimeOpen: true, VpnEnabled: true}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{firstUser, secondUser}, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return([]postgres.ListEventLabAccessLabsRow{{LabGroupName: testLabGroup(eventID, teamID), LabName: "c-ready", Available: true}}, nil)
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	if err := uc.ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatalf("ReconcilePendingLabAccess: %v", err)
	}
	if len(infra.policies) != 2 {
		t.Fatalf("policies=%+v", infra.policies)
	}
	if got, want := infra.operations, []string{"ensure", "policy", "resume", "group-resume"}; !sameLabAccessOperations(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
	for _, policy := range infra.policies {
		if len(policy.AllowedLabs) != 1 || policy.AllowedLabs[0] != "c-ready" {
			t.Fatalf("preparing lab leaked into policy: %+v", policy)
		}
	}
}

func TestReconcilePendingLabAccessRevokesUnavailableLabs(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, AppliedRevision: 1, UpdatedAt: now, RuntimeOpen: false, VpnEnabled: true}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{userID}, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return([]postgres.ListEventLabAccessLabsRow{{LabGroupName: testLabGroup(eventID, teamID), LabName: "c-preparing", Available: false}}, nil)
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	if err := uc.ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatalf("ReconcilePendingLabAccess: %v", err)
	}
	if len(infra.policies) != 1 || len(infra.policies[0].AllowedLabs) != 0 {
		t.Fatalf("unavailable Lab was not revoked: %+v", infra.policies)
	}
	if got, want := infra.operations, []string{"ensure", "policy", "resume", "group-resume"}; !sameLabAccessOperations(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
}

func TestReconcilePendingLabAccessKeepsTestTunnelBeforeAnyLab(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 1, VpnEnabled: true}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{userID}, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return(nil, nil)
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if err := uc.ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := infra.operations, []string{"ensure", "policy", "resume", "group-resume"}; !sameLabAccessOperations(got, want) {
		t.Fatalf("operations=%v want=%v", got, want)
	}
	if len(infra.policies) != 1 || len(infra.policies[0].AllowedLabs) != 0 {
		t.Fatalf("zero-lab group must have default-deny ACL: %+v", infra.policies)
	}
}

func TestReconcilePendingLabAccessDisablesExistingTunnel(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, VpnEnabled: false}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return(nil, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return(nil, nil)
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if err := uc.ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := infra.operations, []string{"suspend", "group-suspend"}; !sameLabAccessOperations(got, want) {
		t.Fatalf("operations=%v want=%v", got, want)
	}
}

func TestReconcilePendingLabAccessVPNOffStopsGroupServices(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, RuntimeOpen: true, VpnEnabled: false}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{userID}, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return([]postgres.ListEventLabAccessLabsRow{{LabGroupName: testLabGroup(eventID, teamID), LabName: "internet-only", Available: true}}, nil)
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if err := uc.ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got, want := infra.operations, []string{"suspend", "group-suspend", "policy"}; !sameLabAccessOperations(got, want) {
		t.Fatalf("operations=%v want=%v", got, want)
	}
	// The policy also gates the web proxy, so the member keeps the available lab
	// while the VPN itself is stopped.
	if len(infra.policies) != 1 || len(infra.policies[0].AllowedLabs) != 1 {
		t.Fatalf("policy must list the member with the available lab: %+v", infra.policies)
	}
}

func TestReconcilePendingLabAccess_AgentErrorLeavesRevisionDirty(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{err: errors.New("agent unavailable")}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, AppliedRevision: 1, UpdatedAt: now, RuntimeOpen: true, VpnEnabled: true}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{userID}, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return([]postgres.ListEventLabAccessLabsRow{{LabGroupName: testLabGroup(eventID, teamID), LabName: "c-ready", Available: true}}, nil)

	if err := uc.ReconcilePendingLabAccess(context.Background()); err == nil {
		t.Fatal("agent failure acknowledged a dirty revision")
	}
}

func TestReconcilePendingLabAccess_SuspendErrorLeavesRevisionDirty(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{suspendErr: errors.New("agent unavailable")}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, AppliedRevision: 1, UpdatedAt: now, RuntimeOpen: false}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return(nil, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return([]postgres.ListEventLabAccessLabsRow{{LabGroupName: testLabGroup(eventID, teamID), LabName: "c-preparing", Available: false}}, nil)

	if err := uc.ReconcilePendingLabAccess(context.Background()); err == nil {
		t.Fatal("suspend failure acknowledged a dirty revision")
	}
	if got, want := infra.operations, []string{"suspend"}; !sameLabAccessOperations(got, want) {
		t.Fatalf("operations = %v, want %v", got, want)
	}
}

func sameLabAccessOperations(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func testLabGroup(eventID, teamID uuid.UUID) string {
	group, err := labBindingModel.GroupName(eventID, teamID)
	if err != nil {
		panic(err)
	}
	return group
}

func TestReconcilePendingLabAccessListsMembersWithVPNOff(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID, firstUser, secondUser := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, AppliedRevision: 1, RuntimeOpen: true, VpnEnabled: false}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{firstUser, secondUser}, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return([]postgres.ListEventLabAccessLabsRow{{LabGroupName: testLabGroup(eventID, teamID), LabName: "web-task", Available: true}}, nil)
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	if err := uc.ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(infra.policies) != 2 {
		t.Fatalf("every approved member must be listed with the VPN off: %+v", infra.policies)
	}
	for _, policy := range infra.policies {
		if len(policy.AllowedLabs) != 1 || policy.AllowedLabs[0] != "web-task" {
			t.Fatalf("web lab must be allowed without the VPN: %+v", policy)
		}
	}
}

type clientCreatingInfra struct {
	recordingLabAccessInfra
	created []string
}

func (i *clientCreatingInfra) EnsureLabClient(_ context.Context, group, client string) (string, error) {
	i.created = append(i.created, group+"/"+client)
	return "config-of-" + client, nil
}

type mapVPNStore struct{ configs map[uuid.UUID]string }

func (s *mapVPNStore) StoreConfig(_ context.Context, userID uuid.UUID, _ vpnModel.Scope, _ uuid.NullUUID, config string) error {
	s.configs[userID] = config
	return nil
}
func (s *mapVPNStore) GetConfig(_ context.Context, userID uuid.UUID, _ vpnModel.Scope, _ uuid.NullUUID) (string, error) {
	return s.configs[userID], nil
}

// Every member gets a LabGroupClient as soon as the sync runs (they are in the
// team), before any lab is open; a member who already has one keeps it.
func TestReconcilePendingLabAccessCreatesMemberClientsEagerly(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &clientCreatingInfra{}
	newMember, oldMember := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	store := &mapVPNStore{configs: map[uuid.UUID]string{oldMember: "kept"}}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra, VPN: store})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 1, UpdatedAt: now, RuntimeOpen: false, VpnEnabled: true}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{newMember, oldMember}, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return(nil, nil)
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	if err := uc.ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatalf("ReconcilePendingLabAccess: %v", err)
	}
	group, _ := labBindingModel.GroupName(eventID, teamID)
	if len(infra.created) != 1 || infra.created[0] != group+"/"+labBindingModel.ParticipantClientName(newMember) {
		t.Fatalf("clients created = %v, want only the new member", infra.created)
	}
	if store.configs[newMember] == "" || store.configs[oldMember] != "kept" {
		t.Fatalf("stored configs = %v", store.configs)
	}
}

// Group and client names stay inside the 63 characters of a Kubernetes label.
func TestLabNamesFitKubernetesLabels(t *testing.T) {
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	group, err := labBindingModel.GroupName(eventID, teamID)
	if err != nil || len(group) > 63 {
		t.Fatalf("group %q (%d) err=%v", group, len(group), err)
	}
	gotEvent, gotTeam, ok := labBindingModel.ParseGroupName(group)
	if !ok || gotEvent != eventID || gotTeam != teamID {
		t.Fatalf("group %q does not round-trip: %v %v %v", group, gotEvent, gotTeam, ok)
	}
	if client := labBindingModel.ParticipantClientName(eventID); len(client) > 63 {
		t.Fatalf("client name %q is too long", client)
	}
}

func TestReconcilePendingLabAccessKeepsRevisionDirtyWhileGroupTerminating(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{err: &infraModel.TerminatingError{Message: "LabGroup is still being deleted", RetryAfter: time.Second}}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return([]postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, AppliedRevision: 1, UpdatedAt: time.Now(), RuntimeOpen: true, VpnEnabled: true}}, nil)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{userID}, nil)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return([]postgres.ListEventLabAccessLabsRow{{LabGroupName: testLabGroup(eventID, teamID), LabName: "c-ready", Available: true}}, nil)
	// MarkEventLabAccessSyncApplied is not expected: the revision must stay dirty.

	err := uc.ReconcilePendingLabAccess(context.Background())
	if _, ok := infraModel.AsTerminating(err); !ok {
		t.Fatalf("terminating error must reach the worker unchanged in kind, got %v", err)
	}
}

func TestReconcilePendingLabAccess_FailingTeamBacksOffAndDoesNotBlockOthers(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &recordingLabAccessInfra{err: errors.New("agent down")}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	otherTeam := uuid.Must(uuid.NewV7())
	now := time.Now()
	rows := []postgres.ListDirtyEventLabAccessSyncsRow{
		{EventTeamID: teamID, EventID: eventID, DesiredRevision: 2, UpdatedAt: now, RuntimeOpen: true, VpnEnabled: true},
		{EventTeamID: otherTeam, EventID: eventID, DesiredRevision: 2, UpdatedAt: now, RuntimeOpen: true, VpnEnabled: false},
	}
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return(rows, nil).Times(2)
	// first pass: the failing team is tried once, the next team still runs
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return(nil, nil).Times(1)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return(nil, nil).Times(1)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), otherTeam).Return(nil, nil).Times(2)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), otherTeam).Return(nil, nil).Times(2)
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(2)

	if err := uc.ReconcilePendingLabAccess(context.Background()); err == nil {
		t.Fatal("the failing team's error is reported")
	}
	// second pass right away: the failing team waits out its backoff (no new agent call, no new error)
	if err := uc.ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatalf("a team in backoff is skipped silently: %v", err)
	}
}

// A revocation is never taken for done while the group is not ready: the revision stays dirty (no
// acknowledgement, no error), and when the group is there the policy is built from the state at that moment,
// so the revoked member never gets access.
func TestReconcilePendingLabAccess_RevocationWaitsForGroupAndNeverGrantsRevoked(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	eventID, teamID, kept := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	dirty := []postgres.ListDirtyEventLabAccessSyncsRow{{EventTeamID: teamID, EventID: eventID, DesiredRevision: 3, AppliedRevision: 2, UpdatedAt: now, RuntimeOpen: true, VpnEnabled: true}}
	labs := []postgres.ListEventLabAccessLabsRow{{LabGroupName: testLabGroup(eventID, teamID), LabName: "c-ready", Available: true}}
	// The member was removed before either pass: both read the current roster.
	q.EXPECT().ListDirtyEventLabAccessSyncs(gomock.Any(), int32(100)).Return(dirty, nil).Times(2)
	q.EXPECT().ListEventLabAccessClients(gomock.Any(), teamID).Return([]uuid.UUID{kept}, nil).Times(2)
	q.EXPECT().ListEventLabAccessLabs(gomock.Any(), teamID).Return(labs, nil).Times(2)
	// Only the second pass may acknowledge.
	q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).Return(int64(1), nil).Times(1)

	notReady := &recordingLabAccessInfra{err: fmt.Errorf("replace: %w", infraModel.ErrGroupNotReady)}
	if err := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: notReady}).ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatalf("a wait for the group is not an error: %v", err)
	}
	ready := &recordingLabAccessInfra{}
	if err := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: ready}).ReconcilePendingLabAccess(context.Background()); err != nil {
		t.Fatalf("ReconcilePendingLabAccess: %v", err)
	}
	if len(ready.policies) != 1 || ready.policies[0].Name != notReady.policies[0].Name {
		t.Fatalf("policy must list only the current member, got %+v", ready.policies)
	}
}
