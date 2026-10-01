package event_test

import (
	"context"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"testing"

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
	store, infra := &recordingVPNStore{config: "private-personal-config"}, &earlyVPNInfra{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: store, Infra: infra})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil).Times(2)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).Times(2)
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
