package event_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type invitationTokens struct{}

func (invitationTokens) GenerateSetupToken(context.Context, uuid.UUID, time.Duration) (string, error) {
	return "setup-secret", nil
}

type invitationNotifier struct {
	userID uuid.UUID
	vars   map[string]any
	opts   dispatchModel.NotifyOptions
	typ    notificationTypes.NotificationType
}

func (n *invitationNotifier) Notify(_ context.Context, userID uuid.UUID, payload notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error {
	n.userID = userID
	n.opts = dispatchModel.ApplyNotifyOptions(opts)
	n.typ = payload.NotificationType()
	raw, err := payload.Marshal()
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, &n.vars)
}

func TestInviteParticipantCreatesPendingAccountAndSendsSetupLink(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	notifier := &invitationNotifier{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(notifier)
	eventID, managerID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	email := "new@example.test"
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil)
	q.EXPECT().GetUserByEmail(gomock.Any(), email).Return(postgres.User{}, pgx.ErrNoRows)
	q.EXPECT().CreateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateUserParams) (postgres.User, error) {
		require.Equal(t, email, p.Email)
		require.Equal(t, "incomplete", p.Status)
		userID = p.ID
		return postgres.User{ID: p.ID, Email: p.Email, Status: p.Status}, nil
	})
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.InviteEventParticipantParams) (postgres.EventParticipant, error) {
		require.Equal(t, eventID, p.EventID)
		require.Equal(t, userID, p.UserID)
		require.Equal(t, managerID, p.InvitedBy.UUID)
		return postgres.EventParticipant{EventID: p.EventID, UserID: p.UserID, Status: 1, InvitedBy: p.InvitedBy, Invited: true, CreatedAt: p.CreatedAt}, nil
	})
	results, err := uc.InviteParticipants(context.Background(), eventID, managerID, []event.ParticipantInvitationInput{{Email: email}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Empty(t, results[0].Error)
	require.True(t, unit.saved)
	require.Equal(t, userID, notifier.userID)
	require.Equal(t, eventID, *notifier.opts.ScopeEventID)
	require.Equal(t, []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}, notifier.opts.OverrideChannels)
	link := notifier.vars["invite_url"].(string)
	require.Contains(t, link, "https://id.example.test/setup?token=setup-secret")
	require.Contains(t, link, "return_to=https%3A%2F%2Fctf.example.test%2Finvite")
}

func TestInviteParticipantResendsExistingPendingInvitation(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	notifier := &invitationNotifier{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(notifier)
	eventID, managerID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil)
	q.EXPECT().GetUserByEmail(gomock.Any(), "old@example.test").Return(postgres.User{ID: userID, Email: "old@example.test", Status: "active"}, nil)
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: 1, InvitedBy: uuid.NullUUID{UUID: managerID, Valid: true}, Invited: true, CreatedAt: time.Now(),
	}, nil)
	results, err := uc.InviteParticipants(context.Background(), eventID, managerID, []event.ParticipantInvitationInput{{Email: "old@example.test"}})
	require.NoError(t, err)
	require.Empty(t, results[0].Error)
	require.True(t, unit.saved)
	require.Equal(t, "https://ctf.example.test/invite", notifier.vars["invite_url"])
}

func TestInviteTeamMemberStoresTargetBeforeSending(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	notifier := &invitationNotifier{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(notifier)
	eventID, teamID, managerID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true}}, nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{ID: teamID, EventID: eventID}).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue Team"}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil)
	q.EXPECT().GetUserByEmail(gomock.Any(), "member@example.test").Return(postgres.User{ID: userID, Email: "member@example.test", Status: "active"}, nil)
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.InviteEventParticipantParams) (postgres.EventParticipant, error) {
		require.Equal(t, teamID, p.InvitedTeamID.UUID)
		require.True(t, p.InvitedTeamID.Valid)
		return postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 1, Invited: true, InvitedTeamID: p.InvitedTeamID, InvitedToTeam: true}, nil
	})
	results, err := uc.InviteTeamMembers(context.Background(), eventID, teamID, managerID, []event.ParticipantInvitationInput{{Email: "member@example.test"}})
	require.NoError(t, err)
	require.Empty(t, results[0].Error)
	require.True(t, unit.saved)
	require.Equal(t, "https://ctf.example.test/invite", notifier.vars["invite_url"])
	require.Equal(t, notificationTypes.NotificationType(signalModel.TypeParticipantTeamInvitationSent), notifier.typ)
	require.Equal(t, "Blue Team", notifier.vars["team_name"])
}

func TestAcceptParticipantInvitationApprovesAndPublishesEnrollment(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher }})
	eventID, userID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: 1, InvitedBy: uuid.NullUUID{UUID: managerID, Valid: true}, Invited: true, CreatedAt: now,
	}, nil)
	q.EXPECT().UpdateEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventParticipantParams) (int64, error) {
		require.Equal(t, int16(participantModel.StatusApproved), p.Status)
		return 1, nil
	})
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(registrationOpenEventRow(eventID), nil)
	view, err := uc.AcceptParticipantInvitation(context.Background(), eventID, userID)
	require.NoError(t, err)
	require.Equal(t, participantModel.StatusApproved, view.Status)
	require.True(t, view.Invited)
	require.True(t, unit.saved)
	require.Equal(t, []signalModel.Type{signalModel.TypeParticipantInvitationAccepted, signalModel.TypeParticipantEnrolled}, publisher.types)
}

func TestAcceptTeamInvitationAssignsMembershipAtomically(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher }})
	eventID, teamID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: 1, Invited: true, InvitedTeamID: uuid.NullUUID{UUID: teamID, Valid: true}, InvitedToTeam: true,
	}, nil)
	q.EXPECT().UpdateEventParticipant(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true}, MaxTeamSize: 5}, nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{ID: teamID, EventID: eventID}).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue Team"}, nil)
	q.EXPECT().TryAddEventTeamMember(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.AssignEventParticipantTeamParams) (int64, error) {
		require.Equal(t, teamID, p.TeamID.UUID)
		require.Equal(t, int16(participantModel.TeamRoleMember), p.TeamRole.Int16)
		return 1, nil
	})
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(registrationOpenEventRow(eventID), nil)
	view, err := uc.AcceptParticipantInvitation(context.Background(), eventID, userID)
	require.NoError(t, err)
	require.Equal(t, participantModel.StatusApproved, view.Status)
	require.True(t, unit.saved)
	require.Equal(t, []signalModel.Type{signalModel.TypeParticipantInvitationAccepted, signalModel.TypeParticipantEnrolled}, publisher.types)
}

func TestInviteParticipantsRejectsInvalidAddressWithoutLeakingDetails(t *testing.T) {
	uc := event.NewEventUseCase(event.Dependencies{Repo: newFormGateMock(gomock.NewController(t))})
	results, err := uc.InviteParticipants(context.Background(), uuid.Nil, uuid.Nil, []event.ParticipantInvitationInput{{Email: "bad address"}})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.True(t, strings.Contains(results[0].Error, "Некоректна"))
}

// registrationOpenEventRow is published and not started: invitations are valid.
func registrationOpenEventRow(eventID uuid.UUID) postgres.Event {
	now := time.Now()
	return postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true, PublishAt: now.Add(-time.Hour), StartAt: now.Add(24 * time.Hour)}
}

func TestAcceptParticipantInvitationRejectsClosedWindow(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, SignalPublishers: func(event.IRepository) event.SignalPublisher { return &recordingSignalPublisher{} }})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: 1, Invited: true,
	}, nil)
	started := registrationOpenEventRow(eventID)
	started.StartAt = time.Now().Add(-time.Minute)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
	_, err := uc.AcceptParticipantInvitation(context.Background(), eventID, userID)
	require.ErrorIs(t, err, participantModel.ErrInvitationExpired.Err())
	require.False(t, unit.saved)
}

func TestRevokeParticipantInvitationDeletesAndPublishesSignal(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher }})
	eventID, userID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 1, Invited: true}, nil)
	q.EXPECT().DeleteEventParticipantInvitation(gomock.Any(), postgres.DeleteEventParticipantInvitationParams{EventID: eventID, UserID: userID}).Return(int64(1), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(registrationOpenEventRow(eventID), nil)
	require.NoError(t, uc.RevokeParticipantInvitation(context.Background(), eventID, userID, managerID))
	require.True(t, unit.saved)
	require.Equal(t, []signalModel.Type{signalModel.TypeParticipantInvitationRevoked}, publisher.types)
}

func TestDeclineParticipantInvitationRequiresPendingInvitation(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, SignalPublishers: func(event.IRepository) event.SignalPublisher { return &recordingSignalPublisher{} }})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().DeleteEventParticipantInvitation(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	require.ErrorIs(t, uc.DeclineParticipantInvitation(context.Background(), eventID, userID), participantModel.ErrInvitationRequired.Err())
	require.False(t, unit.saved)
}

func TestResendParticipantInvitationSendsAgain(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	notifier := &invitationNotifier{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(notifier)
	eventID, userID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 1, Invited: true}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(registrationOpenEventRow(eventID), nil)
	q.EXPECT().GetUserByID(gomock.Any(), userID).Return(postgres.User{ID: userID, Email: "member@example.test", Status: "active"}, nil)
	result, err := uc.ResendParticipantInvitation(context.Background(), eventID, userID, managerID)
	require.NoError(t, err)
	require.Equal(t, "member@example.test", result.Email)
	require.NotNil(t, result.SentAt)
	require.Equal(t, "https://ctf.example.test/invite", notifier.vars["invite_url"])
}

// M6: a blocked/deleted account is answered like any other failure: the
// organizer must not read the state of somebody else's account.
func TestInviteParticipantDoesNotRevealAccountState(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(&invitationNotifier{})
	eventID, managerID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil)
	q.EXPECT().GetUserByEmail(gomock.Any(), "blocked@example.test").Return(postgres.User{ID: userID, Email: "blocked@example.test", Status: "blocked"}, nil)

	results, err := uc.InviteParticipants(context.Background(), eventID, managerID, []event.ParticipantInvitationInput{{Email: "blocked@example.test"}})
	require.NoError(t, err)
	require.Equal(t, event.InvitationCodeFailed, results[0].Code, "blocked must look like a plain failure, not account_unavailable")
}

// M6: invitations are bounded per organizer, so a manager cannot use the
// route to mail the world (or probe it) without limit.
func TestInviteParticipantsAreRateLimitedPerOrganizer(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(&invitationNotifier{})
	eventID, managerID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	// Every address is invalid: no repository is touched, but each still counts.
	bad := make([]event.ParticipantInvitationInput, 200)
	for i := range bad {
		bad[i] = event.ParticipantInvitationInput{Email: "not-an-address-" + strings.Repeat("x", i)}
	}
	limited := 0
	for batch := 0; batch < 4; batch++ {
		results, err := uc.InviteParticipants(context.Background(), eventID, managerID, bad)
		require.NoError(t, err)
		for _, r := range results {
			if r.Code == event.InvitationCodeRateLimited {
				limited++
			}
		}
	}
	require.Equal(t, 800-500, limited, "500 per organizer per hour, the rest is refused")
}

func TestInviteParticipantMailToOneAddressIsThrottled(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	notifier := &invitationNotifier{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(notifier)
	eventID, managerID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil).Times(2)
	q.EXPECT().GetUserByEmail(gomock.Any(), "old@example.test").Return(postgres.User{ID: userID, Email: "old@example.test", Status: "active"}, nil).Times(2)
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows).Times(2)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: 1, InvitedBy: uuid.NullUUID{UUID: managerID, Valid: true}, Invited: true, CreatedAt: time.Now(),
	}, nil).Times(2)
	in := []event.ParticipantInvitationInput{{Email: "old@example.test"}}
	first, err := uc.InviteParticipants(context.Background(), eventID, managerID, in)
	require.NoError(t, err)
	require.Empty(t, first[0].Code)
	second, err := uc.InviteParticipants(context.Background(), eventID, managerID, in)
	require.NoError(t, err)
	require.Equal(t, event.InvitationCodeRateLimited, second[0].Code)
}
