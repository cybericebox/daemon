package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func batchUC(q *postgresMocks.MockQuerier, unit *testUoW, notifier *invitationNotifier) *event.EventUseCase {
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, EventDomain: "example.test", IDHost: "id.example.test", SetupTokens: invitationTokens{}})
	uc.SetInvitationNotifier(notifier)
	return uc
}

func expectBatchEvent(q *postgresMocks.MockQuerier, eventID uuid.UUID, maxTeamSize int32, maxTeams pgtype.Int4) {
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize: maxTeamSize, MaxTeams: maxTeams,
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF", LifecycleConfigured: true}, nil)
}

func TestCreateManagedTeams_ReportsInputIssuesWithoutWriting(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := batchUC(q, unit, &invitationNotifier{})
	result, err := uc.CreateManagedTeams(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), []event.BatchTeamInput{
		{Name: "Blue", CaptainEmail: "a@example.test", Members: []event.BatchTeamMemberInput{{Email: "A@example.test"}, {Email: "not-an-email"}}},
		{Name: "Red", CaptainEmail: "nobody@example.test", Members: []event.BatchTeamMemberInput{{Email: "a@example.test"}}},
		{Name: "x", CaptainEmail: "c@example.test", Members: []event.BatchTeamMemberInput{{Email: "c@example.test"}}},
	}, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []event.BatchTeamIssue{
		{Team: 0, Email: "not-an-email", Code: event.BatchIssueEmailInvalid},
		{Team: 1, Email: "a@example.test", Code: event.BatchIssueEmailRepeated},
		{Team: 1, Code: event.BatchIssueCaptain},
		{Team: 2, Code: event.BatchIssueTeamName},
	}, result.Issues)
	require.False(t, unit.saved)
}

// A pending captain and an approved participant: the team starts with an
// empty roster, the approved one joins at once, the captain is invited to the
// team and gets the email only after the commit.
func TestCreateManagedTeams_AssignsApprovedAndInvitesTheRest(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	notifier := &invitationNotifier{}
	uc := batchUC(q, unit, notifier)
	eventID, managerID, memberID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var captainID, teamID uuid.UUID
	expectBatchEvent(q, eventID, 3, pgtype.Int4{Int32: 4, Valid: true})
	q.EXPECT().GetEventTeamByName(gomock.Any(), postgres.GetEventTeamByNameParams{EventID: eventID, Name: "Blue Team"}).Return(postgres.EventTeam{}, pgx.ErrNoRows)
	q.EXPECT().CountEventTeams(gomock.Any(), eventID).Return(int64(3), nil)
	q.EXPECT().GetUserByEmail(gomock.Any(), "cap@example.test").Return(postgres.User{}, pgx.ErrNoRows)
	q.EXPECT().CreateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateUserParams) (postgres.User, error) {
		require.Equal(t, "incomplete", p.Status)
		require.Equal(t, "Olena", p.FirstName)
		require.Equal(t, "Koval", p.LastName)
		captainID = p.ID
		return postgres.User{ID: p.ID, Email: p.Email, FirstName: p.FirstName, LastName: p.LastName, Status: p.Status}, nil
	})
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.GetEventParticipantParams) (postgres.EventParticipant, error) {
		if p.UserID == memberID {
			return postgres.EventParticipant{EventID: eventID, UserID: memberID, Status: int16(participantModel.StatusApproved)}, nil
		}
		return postgres.EventParticipant{}, pgx.ErrNoRows
	}).Times(2)
	q.EXPECT().GetUserByEmail(gomock.Any(), "member@example.test").Return(postgres.User{ID: memberID, Email: "member@example.test", Status: "active"}, nil)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
		require.Equal(t, captainID, arg.CaptainID)
		require.Equal(t, int32(0), arg.MemberCount)
		teamID = arg.ID
		return postgres.EventTeam{ID: arg.ID, EventID: eventID, Name: arg.Name, CaptainID: arg.CaptainID, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
	})
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.InviteEventParticipantParams) (postgres.EventParticipant, error) {
		require.Equal(t, captainID, p.UserID)
		require.Equal(t, teamID, p.InvitedTeamID.UUID)
		return postgres.EventParticipant{EventID: eventID, UserID: p.UserID, Status: 1, Invited: true, InvitedTeamID: p.InvitedTeamID, InvitedToTeam: true}, nil
	})
	q.EXPECT().TryAddEventTeamMember(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.TryAddEventTeamMemberParams) (int64, error) {
		require.Equal(t, int32(3), p.MaxTeamSize)
		return 1, nil
	})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.AssignEventParticipantTeamParams) (int64, error) {
		require.Equal(t, memberID, p.UserID)
		require.Equal(t, int16(participantModel.TeamRoleMember), p.TeamRole.Int16)
		return 1, nil
	})

	result, err := uc.CreateManagedTeams(context.Background(), eventID, managerID, []event.BatchTeamInput{{
		Name: " Blue Team ", CaptainEmail: "Cap@example.test",
		Members: []event.BatchTeamMemberInput{{Email: "cap@example.test", FirstName: "Olena", LastName: "Koval"}, {Email: "member@example.test"}},
	}}, false)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.True(t, unit.saved)
	require.Equal(t, 1, result.Assigned)
	require.Equal(t, 1, result.Invited)
	require.Empty(t, result.NotSent)
	require.Equal(t, []event.BatchTeamOutcome{{ID: teamID, Name: "Blue Team", Created: true}}, result.Teams)
	require.Equal(t, captainID, notifier.userID)
	require.Equal(t, "Blue Team", notifier.vars["team_name"])
}

func TestCreateManagedTeams_RejectsSizeAndTeamLimits(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	uc := batchUC(q, unit, &invitationNotifier{})
	eventID := uuid.Must(uuid.NewV7())
	expectBatchEvent(q, eventID, 2, pgtype.Int4{Int32: 1, Valid: true})
	q.EXPECT().GetEventTeamByName(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{}, pgx.ErrNoRows)
	q.EXPECT().CountEventTeams(gomock.Any(), eventID).Return(int64(1), nil)
	q.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Status: "active"}, nil).AnyTimes()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows).AnyTimes()
	result, err := uc.CreateManagedTeams(context.Background(), eventID, uuid.Must(uuid.NewV7()), []event.BatchTeamInput{{
		Name: "Blue Team", CaptainEmail: "a@example.test",
		Members: []event.BatchTeamMemberInput{{Email: "a@example.test"}, {Email: "b@example.test"}, {Email: "c@example.test"}},
	}}, false)
	require.NoError(t, err)
	require.ElementsMatch(t, []event.BatchTeamIssue{{Team: 0, Code: event.BatchIssueTeamTooLarge}, {Team: -1, Code: event.BatchIssueTooManyTeams}}, result.Issues)
	require.False(t, unit.saved)
}

// Running the same batch again changes nothing: the team is found by name
// with the same captain and everyone is already on it.
func TestCreateManagedTeams_IsIdempotentPerAddress(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	uc := batchUC(q, unit, &invitationNotifier{})
	eventID, teamID, captainID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectBatchEvent(q, eventID, 5, pgtype.Int4{})
	q.EXPECT().GetEventTeamByName(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue Team", CaptainID: captainID}, nil)
	q.EXPECT().GetUserByEmail(gomock.Any(), "cap@example.test").Return(postgres.User{ID: captainID, Email: "cap@example.test", Status: "incomplete"}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: captainID, Status: 1, Invited: true, InvitedTeamID: uuid.NullUUID{UUID: teamID, Valid: true}, InvitedToTeam: true,
	}, nil)
	result, err := uc.CreateManagedTeams(context.Background(), eventID, uuid.Must(uuid.NewV7()), []event.BatchTeamInput{{
		Name: "Blue Team", CaptainEmail: "cap@example.test", Members: []event.BatchTeamMemberInput{{Email: "cap@example.test"}},
	}}, false)
	require.NoError(t, err)
	require.Empty(t, result.Issues)
	require.True(t, unit.saved)
	require.Equal(t, 1, result.Unchanged)
	require.Zero(t, result.Invited)
	require.Equal(t, []event.BatchTeamOutcome{{ID: teamID, Name: "Blue Team"}}, result.Teams)
}

func TestCreateManagedTeams_RejectsMemberOfAnotherTeam(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	uc := batchUC(q, unit, &invitationNotifier{})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expectBatchEvent(q, eventID, 5, pgtype.Int4{})
	q.EXPECT().GetEventTeamByName(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{}, pgx.ErrNoRows)
	q.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).Return(postgres.User{ID: userID, Status: "active"}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), TeamID: uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true},
	}, nil)
	result, err := uc.CreateManagedTeams(context.Background(), eventID, uuid.Must(uuid.NewV7()), []event.BatchTeamInput{{
		Name: "Blue Team", CaptainEmail: "busy@example.test", Members: []event.BatchTeamMemberInput{{Email: "busy@example.test"}},
	}}, false)
	require.NoError(t, err)
	require.Contains(t, result.Issues, event.BatchTeamIssue{Team: 0, Email: "busy@example.test", Code: event.BatchIssueInTeam})
	require.False(t, unit.saved)
}

// An invitee named captain takes the captain seat when accepting.
func TestAcceptTeamInvitationMakesNamedCaptainCaptain(t *testing.T) {
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
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{ID: teamID, EventID: eventID}).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue Team", CaptainID: userID, UpdatedAt: time.Now()}, nil)
	q.EXPECT().TryAddEventTeamMember(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.AssignEventParticipantTeamParams) (int64, error) {
		require.Equal(t, int16(participantModel.TeamRoleCaptain), p.TeamRole.Int16)
		return 1, nil
	})
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(registrationOpenEventRow(eventID), nil)
	_, err := uc.AcceptParticipantInvitation(context.Background(), eventID, userID)
	require.NoError(t, err)
	require.True(t, unit.saved)
	require.Equal(t, []signalModel.Type{signalModel.TypeParticipantInvitationAccepted, signalModel.TypeParticipantEnrolled}, publisher.types)
}

// Revoking the pending captain's invitation hands the seat to a member.
func TestRevokePendingCaptainHandsCaptaincyToMember(t *testing.T) {
	// A bare mock: the shared stubs would answer the roster reads first.
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	unit := &testUoW{}
	publisher := &recordingSignalPublisher{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}, SignalPublishers: func(event.IRepository) event.SignalPublisher { return publisher }})
	eventID, teamID, captainID, memberID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: captainID, Status: 1, Invited: true, InvitedTeamID: uuid.NullUUID{UUID: teamID, Valid: true}, InvitedToTeam: true,
	}, nil)
	q.EXPECT().DeleteEventParticipantInvitation(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Name: "Blue Team", CaptainID: captainID, MemberCount: 1}, nil)
	q.EXPECT().ListEventTeamMembers(gomock.Any(), gomock.Any()).Return([]postgres.ListEventTeamMembersRow{{TeamID: teamID, UserID: memberID}}, nil)
	q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventTeamParams) (int64, error) {
		require.Equal(t, memberID, p.CaptainID)
		return 1, nil
	})
	q.EXPECT().SetEventParticipantTeamRole(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.SetEventParticipantTeamRoleParams) (int64, error) {
		require.Equal(t, memberID, p.UserID)
		require.Equal(t, int16(participantModel.TeamRoleCaptain), p.TeamRole.Int16)
		return 1, nil
	})
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(registrationOpenEventRow(eventID), nil)
	require.NoError(t, uc.RevokeParticipantInvitation(context.Background(), eventID, captainID, uuid.Must(uuid.NewV7())))
	require.True(t, unit.saved)
}

// The CSV preview counts the invitations without committing or sending.
func TestCreateManagedTeams_DryRunCountsWithoutWriting(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	allowNonStaff(q)
	unit := &testUoW{}
	notifier := &invitationNotifier{}
	uc := batchUC(q, unit, notifier)
	eventID := uuid.Must(uuid.NewV7())
	expectBatchEvent(q, eventID, 5, pgtype.Int4{})
	q.EXPECT().GetEventTeamByName(gomock.Any(), gomock.Any()).Return(postgres.EventTeam{}, pgx.ErrNoRows)
	q.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows).Times(2)
	q.EXPECT().CreateUser(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateUserParams) (postgres.User, error) {
		return postgres.User{ID: p.ID, Email: p.Email, Status: p.Status}, nil
	}).Times(2)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows).Times(2)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
		return postgres.EventTeam{ID: arg.ID, EventID: eventID, Name: arg.Name, CaptainID: arg.CaptainID}, nil
	})
	q.EXPECT().InviteEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{Status: 1, Invited: true}, nil).Times(2)
	result, err := uc.CreateManagedTeams(context.Background(), eventID, uuid.Must(uuid.NewV7()), []event.BatchTeamInput{{
		Name: "Blue Team", CaptainEmail: "new@example.test", Members: []event.BatchTeamMemberInput{{Email: "new@example.test"}, {Email: "second@example.test"}},
	}}, true)
	require.NoError(t, err)
	require.Equal(t, 2, result.Invited)
	require.False(t, unit.saved)
	require.True(t, unit.restored)
	require.Equal(t, uuid.Nil, notifier.userID)
}
