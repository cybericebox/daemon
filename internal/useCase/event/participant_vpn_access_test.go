package event_test

import (
	"context"
	"errors"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type earlyVPNInfra struct {
	group, client           string
	groupCalls, clientCalls int
	clientSubnet            string
}

func (*earlyVPNInfra) DeployLab(context.Context, string, string, infraModel.LabMeta, exerciseModel.Topology) error {
	return nil
}
func (*earlyVPNInfra) LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{}, nil
}
func (i *earlyVPNInfra) EnsureVPNGroup(_ context.Context, group string) error {
	i.group, i.groupCalls = group, i.groupCalls+1
	return nil
}
func (i *earlyVPNInfra) EnsureLabClient(_ context.Context, group, client string) (string, error) {
	i.group, i.client, i.clientCalls = group, client, i.clientCalls+1
	return "private-personal-config", nil
}
func (*earlyVPNInfra) DestroyLabGroup(context.Context, string) error { return nil }
func (i *earlyVPNInfra) GetVPNClientSubnet(_ context.Context, group string) (string, error) {
	i.group = group
	return i.clientSubnet, nil
}

func TestOwnVPNProbeStatusRequiresApprovedTeamAndUsesGroupSubnet(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &earlyVPNInfra{clientSubnet: "10.8.7.0/24"}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, Infra: infra})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil)
	q.EXPECT().GetEventTeamLabGroup(gomock.Any(), teamID).Return(readyVPNGroup(eventID, teamID), nil)
	q.EXPECT().GetLabMonitoringCurrent(gomock.Any(), postgres.GetLabMonitoringCurrentParams{EventID: eventID, EventTeamID: teamID, LabGroupName: testLabGroup(eventID, teamID)}).Return(postgres.LabMonitoringCurrent{}, pgx.ErrNoRows)
	status, err := uc.GetOwnVPNProbeStatus(context.Background(), eventID, userID)
	if err != nil || status.GatewayIP != "10.8.7.1" || status.ProbeURL != "http://10.8.7.1:8088/" || infra.group != testLabGroup(eventID, teamID) {
		t.Fatalf("status=%+v group=%q err=%v", status, infra.group, err)
	}
}

// The config is only handed out: the client is created when the person joins a
// team (the access sync), never by a request for the config.
func TestOwnVPNConfigReturnsTheStoredEventScopedSecretAndNeverCreatesAClient(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	q.EXPECT().CreateEventActivity(gomock.Any(), gomock.Any()).Return(nil).AnyTimes() // the download is logged
	store, infra := &recordingVPNStore{config: "private-personal-config"}, &earlyVPNInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: store, Infra: infra})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil).Times(2)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).Times(2)
	q.EXPECT().GetEventTeamLabGroup(gomock.Any(), teamID).Return(readyVPNGroup(eventID, teamID), nil).Times(2)
	for n := 0; n < 2; n++ {
		config, err := uc.GetOwnLabVPNConfig(context.Background(), eventID, userID)
		if err != nil || config != "private-personal-config" {
			t.Fatalf("request %d: config=%q err=%v", n, config, err)
		}
	}
	if infra.groupCalls != 0 || infra.clientCalls != 0 {
		t.Fatalf("a request must not create groups or clients: %d %d", infra.groupCalls, infra.clientCalls)
	}
	if store.userID != userID || store.scope != vpnModel.ScopeEvent || !store.ref.Valid || store.ref.UUID != eventID {
		t.Fatalf("secret read under wrong identity: %+v", store)
	}
}

func TestOwnVPNConfigWithoutAClientIsRefusedNotCreated(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	store, infra := &recordingVPNStore{}, &earlyVPNInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: store, Infra: infra})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil)
	q.EXPECT().GetEventTeamLabGroup(gomock.Any(), teamID).Return(readyVPNGroup(eventID, teamID), nil)
	if config, err := uc.GetOwnLabVPNConfig(context.Background(), eventID, userID); err == nil || config != "" {
		t.Fatalf("config=%q err=%v", config, err)
	}
	if infra.clientCalls != 0 {
		t.Fatal("the missing client must not be created by the request")
	}
}

func TestOwnVPNConfigDeniesDisabledPendingAndTeamless(t *testing.T) {
	for _, tc := range []struct {
		name    string
		useVPN  bool
		status  participantModel.Status
		hasTeam bool
	}{
		{name: "disabled", status: participantModel.StatusApproved, hasTeam: true},
		{name: "pending", useVPN: true, status: participantModel.StatusPending, hasTeam: true},
		{name: "teamless", useVPN: true, status: participantModel.StatusApproved},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			store, infra := &recordingVPNStore{config: "old-secret"}, &earlyVPNInfra{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: store, Infra: infra})
			eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			team := uuid.NullUUID{}
			if tc.hasTeam {
				team = uuid.NullUUID{UUID: teamID, Valid: true}
			}
			q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
				Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(tc.status), TeamID: team}, nil)
			if tc.status == participantModel.StatusApproved && tc.hasTeam {
				q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: tc.useVPN}, nil)
			}
			if config, err := uc.GetOwnLabVPNConfig(context.Background(), eventID, userID); err == nil || config != "" {
				t.Fatalf("unauthorized config=%q err=%v", config, err)
			}
			if infra.groupCalls != 0 || infra.clientCalls != 0 || store.userID != uuid.Nil {
				t.Fatal("denied request touched VPN group, client or stored secret")
			}
		})
	}
}

func readyVPNGroup(eventID, teamID uuid.UUID) postgres.EventTeamGroupAllocation {
	return postgres.EventTeamGroupAllocation{EventID: eventID, EventTeamID: teamID, LabGroupName: testLabGroup(eventID, teamID), AgentUid: "agent-group", DesiredRevision: 2, ObservedRevision: 2, DesiredState: "Running", ActualState: "Running", Ready: true, ObservedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}
}

func TestVPNDownloadRequiresReadyGroupIndependentlyOfChallengeLabs(t *testing.T) {
	for _, name := range []string{"ready without challenge labs", "not ready", "missing group", "stale", "old revision", "stopped", "wrong event", "wrong group name"} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			q.EXPECT().CreateEventActivity(gomock.Any(), gomock.Any()).Return(nil).AnyTimes() // the download is logged
			store, infra := &recordingVPNStore{config: "personal"}, &earlyVPNInfra{}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: store, Infra: infra})
			eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil)
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil)
			group := readyVPNGroup(eventID, teamID)
			var readErr error
			switch name {
			case "not ready":
				group.Ready = false
			case "missing group":
				readErr = pgx.ErrNoRows
			case "stale":
				group.ObservedAt.Time = time.Now().Add(-time.Minute)
			case "old revision":
				group.ObservedRevision--
			case "stopped":
				group.DesiredState = "Stopped"
			case "wrong event":
				group.EventID = uuid.Must(uuid.NewV7())
			case "wrong group name":
				group.LabGroupName = testLabGroup(eventID, uuid.Must(uuid.NewV7()))
			}
			q.EXPECT().GetEventTeamLabGroup(gomock.Any(), teamID).Return(group, readErr)
			config, err := uc.GetOwnLabVPNConfig(context.Background(), eventID, userID)
			if name == "ready without challenge labs" {
				if err != nil || config != "personal" {
					t.Fatalf("config=%q err=%v", config, err)
				}
			} else if err == nil || config != "" || store.userID != uuid.Nil {
				t.Fatalf("unready group exposed config=%q err=%v", config, err)
			}
			if infra.groupCalls != 0 || infra.clientCalls != 0 {
				t.Fatal("download provisioned infrastructure")
			}
		})
	}
}

func TestModeratorVPNDownloadDoesNotCreateAnUnreadyGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &earlyVPNInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: &recordingVPNStore{config: "old"}, Infra: infra})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil)
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true}, nil)
	q.EXPECT().GetEventTeamLabGroup(gomock.Any(), teamID).Return(postgres.EventTeamGroupAllocation{}, pgx.ErrNoRows)
	config, err := uc.GetModeratorsVPNConfig(context.Background(), eventID, userID)
	if err == nil || config != "" || infra.groupCalls != 0 || infra.clientCalls != 0 {
		t.Fatalf("config=%q err=%v infra=%+v", config, err, infra)
	}
}

func TestModeratorVPNWithoutInitializedTeamRemainsUnavailable(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	infra := &earlyVPNInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: &recordingVPNStore{config: "old"}, Infra: infra})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil).Times(2)
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{}, pgx.ErrNoRows).Times(2)
	_, err := uc.GetModeratorsVPNProbeStatus(context.Background(), eventID, userID)
	if !errors.Is(err, eventStandModel.ErrStandModeratorsTeamUnavailable.Err()) {
		t.Fatalf("probe missing-team error=%v", err)
	}
	config, err := uc.GetModeratorsVPNConfig(context.Background(), eventID, userID)
	if config != "" || !errors.Is(err, eventStandModel.ErrStandModeratorsTeamUnavailable.Err()) {
		t.Fatalf("config=%q missing-team error=%v", config, err)
	}
	if infra.groupCalls != 0 || infra.clientCalls != 0 {
		t.Fatal("status/config GET provisioned infrastructure")
	}
}
