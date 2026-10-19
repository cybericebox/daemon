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
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// expectVPNDownloadLogged expects exactly one config download row: who, event and config type.
func expectVPNDownloadLogged(q *postgresMocks.MockQuerier, eventID, userID uuid.UUID, config string) {
	q.EXPECT().CreateEventActivity(gomock.Any(), activityRow(func(p postgres.CreateEventActivityParams) bool {
		var data map[string]string
		return json.Unmarshal(p.Data, &data) == nil && p.Kind == "vpn_config_downloaded" && p.EventID == eventID &&
			p.UserID.UUID == userID && data["config"] == config
	})).Return(nil)
}

func TestParticipantVPNDownloadIsLogged(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: &recordingVPNStore{config: "cfg"}, Infra: &earlyVPNInfra{}})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil)
	q.EXPECT().GetEventTeamLabGroup(gomock.Any(), teamID).Return(readyVPNGroup(eventID, teamID), nil)
	expectVPNDownloadLogged(q, eventID, userID, "participant")
	config, err := uc.GetOwnLabVPNConfig(context.Background(), eventID, userID)
	require.NoError(t, err)
	require.Equal(t, "cfg", config)
}

func TestModeratorVPNDownloadIsLogged(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, VPN: &recordingVPNStore{config: "cfg"}, Infra: &earlyVPNInfra{}})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, InfrastructureAllowed: true}, nil)
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true}, nil)
	q.EXPECT().GetEventTeamLabGroup(gomock.Any(), teamID).Return(readyVPNGroup(eventID, teamID), nil)
	expectVPNDownloadLogged(q, eventID, userID, "moderator")
	config, err := uc.GetModeratorsVPNConfig(context.Background(), eventID, userID)
	require.NoError(t, err)
	require.Equal(t, "cfg", config)
}

func TestPreviewVPNDownloadIsLoggedButStatusIsNot(t *testing.T) {
	f := newPreviewFixture(t, true)
	f.labs.vpnConfig = "preview-cfg"
	ctx := context.Background()
	_, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection, 0)
	require.NoError(t, err)

	// The status route never hands the config out and is polled: no row.
	_, err = f.uc.PreviewLabStatus(ctx, f.event, f.user, f.selection)
	require.NoError(t, err)

	expectVPNDownloadLogged(f.q, f.event, f.user, "preview")
	config, err := f.uc.PreviewVPNConfig(ctx, f.event, f.user, f.selection)
	require.NoError(t, err)
	require.Equal(t, "preview-cfg", config)
}

func TestPreviewVPNWithoutConfigIsNotLogged(t *testing.T) {
	f := newPreviewFixture(t, true)
	ctx := context.Background()
	_, err := f.uc.StartPreviewRun(ctx, f.event, f.user, f.selection, 0)
	require.NoError(t, err)
	config, err := f.uc.PreviewVPNConfig(ctx, f.event, f.user, f.selection)
	require.NoError(t, err)
	require.Empty(t, config)
}

func TestSetEventManagerRefusesAnApprovedParticipant(t *testing.T) {
	for _, role := range []eventManagerModel.Role{eventManagerModel.RoleManager, eventManagerModel.RoleViewer} {
		q := newFormGateMock(gomock.NewController(t))
		publisher := &recordingSignalPublisher{}
		uc := event.NewEventUseCase(event.Dependencies{
			Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}},
			SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher },
		})
		eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
			Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved)}, nil)
		_, err := uc.SetEventManager(context.Background(), eventID, event.SetEventManagerInput{UserID: userID, Role: int16(role)})
		require.ErrorIs(t, err, eventManagerModel.ErrEventManagerIsParticipant.Err())
		require.Empty(t, publisher.types)
	}
}

// A pending (not approved) participation does not block the assignment.
func TestSetEventManagerAllowsAPendingParticipant(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending)}, nil)
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows)
	q.EXPECT().UpsertEventManager(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a postgres.UpsertEventManagerParams) (postgres.EventManager, error) {
		return postgres.EventManager{EventID: a.EventID, UserID: a.UserID, Role: a.Role, CreatedAt: time.Now()}, nil
	})
	view, err := uc.SetEventManager(context.Background(), eventID, event.SetEventManagerInput{UserID: userID, Role: int16(eventManagerModel.RoleManager)})
	require.NoError(t, err)
	require.Equal(t, int16(eventManagerModel.RoleManager), view.Role)
}

// recordingLabClientInfra records the lab clients deleted by the access cleanup.
type recordingLabClientInfra struct {
	earlyVPNInfra

	deleted []string
}

func (i *recordingLabClientInfra) DeleteLabClient(_ context.Context, group, client string) error {
	i.deleted = append(i.deleted, group+"/"+client)
	return nil
}

func TestDemotingAModeratorToViewerDropsTheLabClient(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	infra, store := &recordingLabClientInfra{}, &recordingVPNStore{config: "cfg"}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, Infra: infra, VPN: store,
		SignalPublishers: func(event.IRepository) event.SignalPublisher { return &recordingSignalPublisher{} },
	})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetUserByID(gomock.Any(), userID).Return(postgres.User{ID: userID, Role: "user"}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventManager{EventID: eventID, UserID: userID, Role: int16(eventManagerModel.RoleManager), CreatedAt: now}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "t", Name: "n", AvailableFrom: now}, nil)
	q.EXPECT().UpsertEventManager(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a postgres.UpsertEventManagerParams) (postgres.EventManager, error) {
		return postgres.EventManager(a), nil
	})
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true}, nil)
	_, err := uc.SetEventManager(context.Background(), eventID, event.SetEventManagerInput{UserID: userID, Role: int16(eventManagerModel.RoleViewer)})
	require.NoError(t, err)
	require.Len(t, infra.deleted, 1)
}

func TestPromotingOrKeepingARoleKeepsTheLabClient(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	infra := &recordingLabClientInfra{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, Infra: infra,
		SignalPublishers: func(event.IRepository) event.SignalPublisher { return &recordingSignalPublisher{} },
	})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{EventID: eventID, UserID: userID, Role: int16(eventManagerModel.RoleViewer), CreatedAt: now}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "t", Name: "n", AvailableFrom: now}, nil)
	q.EXPECT().UpsertEventManager(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, a postgres.UpsertEventManagerParams) (postgres.EventManager, error) {
		return postgres.EventManager(a), nil
	})
	_, err := uc.SetEventManager(context.Background(), eventID, event.SetEventManagerInput{UserID: userID, Role: int16(eventManagerModel.RoleManager)})
	require.NoError(t, err)
	require.Empty(t, infra.deleted)
}
