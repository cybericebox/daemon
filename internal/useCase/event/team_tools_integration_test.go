package event_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// toolsTokens records the setup links that were issued and revoked.
type toolsTokens struct {
	mu      sync.Mutex
	issued  []uuid.UUID
	revoked []uuid.UUID
}

func (t *toolsTokens) GenerateSetupToken(
	_ context.Context,
	userID uuid.UUID,
	_ time.Duration,
) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.issued = append(t.issued, userID)
	return "setup-" + userID.String(), nil
}

func (t *toolsTokens) Revoke(_ context.Context, userID uuid.UUID) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.revoked = append(t.revoked, userID)
	return nil
}

// toolsFixture is the organizer's roster tooling over a real database: an event with team participation (2..5
// members), formed teams and a way to put people in them.
type toolsFixture struct {
	*standFixture

	uc       *event.EventUseCase
	tokens   *toolsTokens
	notifier *invitationNotifier
}

func newToolsFixture(t *testing.T) *toolsFixture {
	t.Helper()
	f := &toolsFixture{
		standFixture: newStandFixture(t),
		tokens:       &toolsTokens{},
		notifier:     &invitationNotifier{},
	}
	f.uc = event.NewEventUseCase(event.Dependencies{
		Repo: f.db.Queries,
		UoW: postgres.NewUnitOfWorker[event.IRepository](
			postgres.NewUoWFactory(f.db.Pool),
		),
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now),
		EventDomain:      "example.test",
		IDHost:           "id.example.test",
		SetupTokens:      f.tokens,
	})
	f.uc.SetInvitationNotifier(f.notifier)
	return f
}

func (f *toolsFixture) user(t *testing.T, email string) uuid.UUID {
	t.Helper()
	u, err := userRepo.New(f.db.Queries).
		Create(context.Background(), userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, time.Now()))
	require.NoError(t, err)
	return u.ID
}

// approved makes an approved participant without a team.
func (f *toolsFixture) approved(t *testing.T, email string) uuid.UUID {
	t.Helper()
	id := f.user(t, email)
	now := time.Now()
	_, ok, err := participantRepo.New(f.db.Queries).
		Upsert(context.Background(), participantModel.Participant{
			EventID:   f.eventID,
			UserID:    id,
			Status:    participantModel.StatusApproved,
			CreatedAt: now,
			DecidedAt: &now,
		})
	require.NoError(t, err)
	require.True(t, ok)
	return id
}

// newTeam builds a formed team with an approved captain.
func (f *toolsFixture) newTeam(t *testing.T, name string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	captainID := f.approved(t, name+"-captain@tools.test")
	now := time.Now()
	team, err := eventTeamModel.New(f.eventID, captainID, name, "tools-code-"+name, now)
	require.NoError(t, err)
	team.FormedAt = &now
	stored, err := eventTeamRepo.New(f.db.Queries).Create(ctx, team)
	require.NoError(t, err)
	captain, err := participantRepo.New(f.db.Queries).Get(ctx, f.eventID, captainID)
	require.NoError(t, err)
	require.NoError(t, captain.AssignTeam(stored.ID, participantModel.TeamRoleCaptain))
	affected, err := participantRepo.New(f.db.Queries).AssignTeam(ctx, captain)
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)
	return stored.ID, captainID
}

// member adds an approved member to the team.
func (f *toolsFixture) member(t *testing.T, teamID uuid.UUID, email string) uuid.UUID {
	t.Helper()
	require.NoError(
		t,
		f.uc.AssignParticipantToTeam(context.Background(), f.eventID, teamID, f.approved(t, email)),
	)
	var id uuid.UUID
	require.NoError(
		t,
		f.db.Pool.QueryRow(context.Background(), `SELECT id FROM users WHERE email = $1`, email).
			Scan(&id),
	)
	return id
}

func (f *toolsFixture) placement(t *testing.T, userID uuid.UUID) (*uuid.UUID, *int16, int16) {
	t.Helper()
	var teamID *uuid.UUID
	var role *int16
	var status int16
	require.NoError(t, f.db.Pool.QueryRow(
		context.Background(),
		`SELECT team_id, team_role, status FROM event_participants WHERE event_id = $1 AND user_id = $2`,
		f.eventID,
		userID,
	).Scan(&teamID, &role, &status))
	return teamID, role, status
}

func (f *toolsFixture) memberCount(t *testing.T, teamID uuid.UUID) int {
	t.Helper()
	return f.count(t, `SELECT member_count FROM event_teams WHERE id = $1`, teamID)
}

// started puts the event after its start (rosters of formed teams are closed to the members themselves).
func (f *toolsFixture) started(t *testing.T) {
	t.Helper()
	f.shiftLifecycle(t, -time.Hour, time.Hour)
}

func TestOrganizerMovesAMemberBetweenFormedTeamsAfterTheStart(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	redID, _ := f.newTeam(t, "Alpha")
	blueID, _ := f.newTeam(t, "Bravo")
	member := f.member(t, redID, "walker@tools.test")
	f.started(t)
	require.Equal(t, 2, f.memberCount(t, redID))

	require.NoError(t, f.uc.MoveParticipantToTeam(ctx, f.eventID, member, blueID))

	team, role, status := f.placement(t, member)
	require.NotNil(t, team)
	require.Equal(t, blueID, *team)
	require.EqualValues(t, participantModel.TeamRoleMember, *role)
	require.EqualValues(
		t,
		participantModel.StatusApproved,
		status,
		"a move keeps the person in the event",
	)
	require.Equal(t, 1, f.memberCount(t, redID))
	require.Equal(t, 2, f.memberCount(t, blueID))

	// moving to the team the person is already in changes nothing
	require.NoError(t, f.uc.MoveParticipantToTeam(ctx, f.eventID, member, blueID))
	require.Equal(t, 2, f.memberCount(t, blueID))
}

func TestMovingNeedsAMemberASeatAndARealTarget(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	redID, redCaptain := f.newTeam(t, "Alpha")
	blueID, _ := f.newTeam(t, "Bravo")
	member := f.member(t, redID, "walker@tools.test")

	err := f.uc.MoveParticipantToTeam(ctx, f.eventID, redCaptain, blueID)
	require.ErrorIs(
		t,
		err,
		eventTeamModel.ErrEventTeamCaptainMustTransfer.Err(),
		"the captain hands over the captaincy first",
	)

	err = f.uc.MoveParticipantToTeam(ctx, f.eventID, member, uuid.Must(uuid.NewV7()))
	require.ErrorIs(t, err, eventTeamModel.ErrEventTeamNotFound.Err())
	team, _, _ := f.placement(t, member)
	require.Equal(t, redID, *team, "a failed move leaves the person where they were")
	require.Equal(t, 2, f.memberCount(t, redID))

	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE event_configs SET max_team_size = 2 WHERE event_id = $1`,
		f.eventID,
	)
	require.NoError(t, err)
	f.member(t, blueID, "blue-two@tools.test")
	err = f.uc.MoveParticipantToTeam(ctx, f.eventID, member, blueID)
	require.ErrorIs(t, err, eventTeamModel.ErrEventTeamFull.Err())
	team, _, _ = f.placement(t, member)
	require.Equal(t, redID, *team)
	require.Equal(
		t,
		2,
		f.memberCount(t, redID),
		"the source count is not touched by a refused move",
	)

	err = f.uc.MoveParticipantToTeam(ctx, f.eventID, f.approved(t, "loner@tools.test"), blueID)
	require.ErrorIs(
		t,
		err,
		participantModel.ErrParticipantTeamInvalid.Err(),
		"someone without a team is assigned, not moved",
	)
}

func TestAssigningAMemberOfAnotherTeamMovesThem(t *testing.T) {
	f := newToolsFixture(t)
	redID, _ := f.newTeam(t, "Alpha")
	blueID, _ := f.newTeam(t, "Bravo")
	member := f.member(t, redID, "walker@tools.test")

	require.NoError(
		t,
		f.uc.AssignParticipantToTeam(context.Background(), f.eventID, blueID, member),
	)

	team, _, _ := f.placement(t, member)
	require.Equal(t, blueID, *team)
	require.Equal(t, 1, f.memberCount(t, redID))
	require.Equal(t, 2, f.memberCount(t, blueID))
}

func TestOrganizerChangesTheCaptainToAMemberOrAPendingInvitee(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, captain := f.newTeam(t, "Alpha")
	member := f.member(t, teamID, "walker@tools.test")
	f.started(t)

	require.NoError(t, f.uc.TransferManagedTeamCaptaincy(ctx, f.eventID, teamID, member))
	_, role, _ := f.placement(t, member)
	require.EqualValues(t, participantModel.TeamRoleCaptain, *role)
	_, role, _ = f.placement(t, captain)
	require.EqualValues(t, participantModel.TeamRoleMember, *role)
	team, err := eventTeamRepo.New(f.db.Queries).GetByID(ctx, f.eventID, teamID)
	require.NoError(t, err)
	require.Equal(t, member, team.CaptainID)

	// someone who is neither a member nor invited to this team cannot lead it
	outsider := f.approved(t, "outsider@tools.test")
	require.ErrorIs(
		t,
		f.uc.TransferManagedTeamCaptaincy(ctx, f.eventID, teamID, outsider),
		eventTeamModel.ErrEventTeamCaptainInvalid.Err(),
	)

	// a pending invitee of the team takes the seat on accepting
	results, err := f.uc.InviteTeamMembers(
		ctx,
		f.eventID,
		teamID,
		f.ownerID,
		[]event.ParticipantInvitationInput{{Email: "invitee@tools.test"}},
		true,
	)
	require.NoError(t, err)
	require.Empty(t, results[0].Code)
	invitee := results[0].UserID
	require.NoError(t, f.uc.TransferManagedTeamCaptaincy(ctx, f.eventID, teamID, invitee))
	_, role, _ = f.placement(t, member)
	require.EqualValues(
		t,
		participantModel.TeamRoleMember,
		*role,
		"the former captain becomes a member",
	)
	_, err = f.uc.AcceptParticipantInvitationFor(ctx, f.eventID, invitee, f.ownerID)
	require.NoError(t, err)
	_, role, _ = f.placement(t, invitee)
	require.EqualValues(
		t,
		participantModel.TeamRoleCaptain,
		*role,
		"accepting takes the captain's seat",
	)
}

// A team an organizer built around a captain who has not accepted yet: the captain is only invited, so there is no
// seat to give up when the organizer names somebody else.
func TestChangingThePendingCaptainOfATeamNeedsNoSeatToGiveUp(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, _ := f.newTeam(t, "Alpha")
	pendingCaptain := f.user(t, "pending-captain@tools.test")
	_, ok, err := participantRepo.New(f.db.Queries).
		Invite(ctx, f.eventID, pendingCaptain, f.ownerID, uuid.NullUUID{UUID: teamID, Valid: true}, time.Now())
	require.NoError(t, err)
	require.True(t, ok)
	team, err := eventTeamRepo.New(f.db.Queries).GetByID(ctx, f.eventID, teamID)
	require.NoError(t, err)
	expected := team.UpdatedAt
	team.CaptainID, team.UpdatedAt = pendingCaptain, time.Now()
	affected, err := eventTeamRepo.New(f.db.Queries).Update(ctx, team, expected)
	require.NoError(t, err)
	require.EqualValues(t, 1, affected)
	// the real former captain is still seated as the captain; they are not the team's captain any more
	member := f.member(t, teamID, "walker@tools.test")

	require.NoError(t, f.uc.TransferManagedTeamCaptaincy(ctx, f.eventID, teamID, member))

	_, role, _ := f.placement(t, member)
	require.EqualValues(t, participantModel.TeamRoleCaptain, *role)
	team, err = eventTeamRepo.New(f.db.Queries).GetByID(ctx, f.eventID, teamID)
	require.NoError(t, err)
	require.Equal(t, member, team.CaptainID)
}

func TestOrganizerAcceptsAnInvitationForTheInvitedPersonWhateverTheWindow(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, _ := f.newTeam(t, "Alpha")
	results, err := f.uc.InviteTeamMembers(
		ctx,
		f.eventID,
		teamID,
		f.ownerID,
		[]event.ParticipantInvitationInput{{Email: "late@tools.test"}},
		true,
	)
	require.NoError(t, err)
	invitee := results[0].UserID
	// the event is over: registration is closed and the team is formed
	f.shiftLifecycle(t, -3*time.Hour, -time.Hour)

	_, err = f.uc.AcceptParticipantInvitation(ctx, f.eventID, invitee)
	require.ErrorIs(
		t,
		err,
		participantModel.ErrInvitationExpired.Err(),
		"the person themselves are time-limited",
	)

	view, err := f.uc.AcceptParticipantInvitationFor(ctx, f.eventID, invitee, f.ownerID)
	require.NoError(t, err)
	require.Equal(t, participantModel.StatusApproved, view.Status)
	team, role, status := f.placement(t, invitee)
	require.Equal(t, teamID, *team)
	require.EqualValues(t, participantModel.TeamRoleMember, *role)
	require.EqualValues(t, participantModel.StatusApproved, status)
	require.Equal(t, 2, f.memberCount(t, teamID))

	_, err = f.uc.AcceptParticipantInvitationFor(ctx, f.eventID, invitee, f.ownerID)
	require.ErrorIs(
		t,
		err,
		participantModel.ErrInvitationRequired.Err(),
		"nothing is pending any more",
	)
}

func TestAcceptingForSomeoneStillRespectsTheTeamSize(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, _ := f.newTeam(t, "Alpha")
	_, err := f.db.Pool.Exec(
		ctx,
		`UPDATE event_configs SET max_team_size = 2 WHERE event_id = $1`,
		f.eventID,
	)
	require.NoError(t, err)
	f.member(t, teamID, "walker@tools.test")
	invitee := f.user(t, "extra@tools.test")
	_, ok, err := participantRepo.New(f.db.Queries).
		Invite(ctx, f.eventID, invitee, f.ownerID, uuid.NullUUID{UUID: teamID, Valid: true}, time.Now())
	require.NoError(t, err)
	require.True(t, ok)

	_, err = f.uc.AcceptParticipantInvitationFor(ctx, f.eventID, invitee, f.ownerID)

	require.ErrorIs(t, err, eventTeamModel.ErrEventTeamFull.Err())
	_, _, status := f.placement(t, invitee)
	require.EqualValues(
		t,
		participantModel.StatusPending,
		status,
		"a refused acceptance leaves the invitation pending",
	)
}

func TestOrganizerRevokesAnInviteAndAPendingCaptainHandsTheTeamToTheNext(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, captain := f.newTeam(t, "Alpha")
	results, err := f.uc.InviteTeamMembers(
		ctx,
		f.eventID,
		teamID,
		f.ownerID,
		[]event.ParticipantInvitationInput{{Email: "gone@tools.test"}},
		true,
	)
	require.NoError(t, err)
	gone := results[0].UserID

	require.NoError(t, f.uc.RevokeParticipantInvitation(ctx, f.eventID, gone, f.ownerID))
	require.Zero(
		t,
		f.count(
			t,
			`SELECT count(*) FROM event_participants WHERE event_id = $1 AND user_id = $2`,
			f.eventID,
			gone,
		),
	)
	require.ErrorIs(
		t,
		f.uc.RevokeParticipantInvitation(ctx, f.eventID, gone, f.ownerID),
		participantModel.ErrInvitationRequired.Err(),
	)

	// an accepted participant is not an invitation
	require.ErrorIs(
		t,
		f.uc.RevokeParticipantInvitation(ctx, f.eventID, captain, f.ownerID),
		participantModel.ErrInvitationRequired.Err(),
	)
}

func TestChangingTheEmailOfAPendingInvitationMovesItAndKillsTheOldLink(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, _ := f.newTeam(t, "Alpha")
	results, err := f.uc.InviteTeamMembers(
		ctx,
		f.eventID,
		teamID,
		f.ownerID,
		[]event.ParticipantInvitationInput{
			{Email: "typo@tools.test", FirstName: "Ira", LastName: "Koval"},
		},
		true,
	)
	require.NoError(t, err)
	wrong := results[0].UserID

	result, err := f.uc.ChangeParticipantInvitationEmail(
		ctx,
		f.eventID,
		wrong,
		f.ownerID,
		"  Right@Tools.Test ",
		false,
	)
	require.NoError(t, err)

	require.Equal(t, "right@tools.test", result.Email)
	require.NotEqual(t, wrong, result.UserID)
	require.NotNil(t, result.SentAt)
	require.Zero(
		t,
		f.count(
			t,
			`SELECT count(*) FROM event_participants WHERE event_id = $1 AND user_id = $2`,
			f.eventID,
			wrong,
		),
		"the wrong address holds no invitation",
	)
	team, _, status := f.placement(t, result.UserID)
	require.Nil(t, team)
	require.EqualValues(t, participantModel.StatusPending, status)
	var invitedTeam uuid.UUID
	var invitedToTeam bool
	require.NoError(
		t,
		f.db.Pool.QueryRow(ctx, `SELECT invited_team_id, invited_to_team FROM event_participants WHERE event_id = $1 AND user_id = $2`, f.eventID, result.UserID).
			Scan(&invitedTeam, &invitedToTeam),
	)
	require.True(t, invitedToTeam)
	require.Equal(t, teamID, invitedTeam, "the team goes with the invitation")
	require.Equal(t, result.UserID, f.notifier.userID, "the new email goes to the new address")
	require.Contains(t, f.tokens.issued, result.UserID)
	require.Equal(
		t,
		[]uuid.UUID{wrong},
		f.tokens.revoked,
		"the link sent to the wrong address stops working",
	)
	var first, last string
	require.NoError(
		t,
		f.db.Pool.QueryRow(ctx, `SELECT first_name, last_name FROM users WHERE id = $1`, result.UserID).
			Scan(&first, &last),
	)
	require.Equal(
		t,
		[2]string{"Ira", "Koval"},
		[2]string{first, last},
		"the typed name moves with the invitation",
	)
}

func TestChangingTheEmailOfAPendingCaptainMovesTheCaptaincy(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, _ := f.newTeam(t, "Alpha")
	results, err := f.uc.InviteTeamMembers(
		ctx,
		f.eventID,
		teamID,
		f.ownerID,
		[]event.ParticipantInvitationInput{{Email: "typo@tools.test"}},
		true,
	)
	require.NoError(t, err)
	wrong := results[0].UserID
	require.NoError(t, f.uc.TransferManagedTeamCaptaincy(ctx, f.eventID, teamID, wrong))

	result, err := f.uc.ChangeParticipantInvitationEmail(
		ctx,
		f.eventID,
		wrong,
		f.ownerID,
		"right@tools.test",
		true,
	)
	require.NoError(t, err)

	team, err := eventTeamRepo.New(f.db.Queries).GetByID(ctx, f.eventID, teamID)
	require.NoError(t, err)
	require.Equal(t, result.UserID, team.CaptainID)
	require.Nil(t, result.SentAt, "no email was asked for")
}

func TestChangingTheEmailRefusesWhatCannotBeInvited(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, captain := f.newTeam(t, "Alpha")
	results, err := f.uc.InviteTeamMembers(
		ctx,
		f.eventID,
		teamID,
		f.ownerID,
		[]event.ParticipantInvitationInput{{Email: "typo@tools.test"}},
		true,
	)
	require.NoError(t, err)
	wrong := results[0].UserID

	_, err = f.uc.ChangeParticipantInvitationEmail(
		ctx,
		f.eventID,
		wrong,
		f.ownerID,
		"not an address",
		true,
	)
	require.ErrorIs(t, err, participantModel.ErrInvitationEmailInvalid.Err())

	_, err = f.uc.ChangeParticipantInvitationEmail(
		ctx,
		f.eventID,
		wrong,
		f.ownerID,
		"Alpha-captain@tools.test",
		true,
	)
	require.ErrorIs(
		t,
		err,
		participantModel.ErrAlreadyParticipant.Err(),
		"the new address already takes part",
	)
	require.Equal(
		t,
		1,
		f.count(
			t,
			`SELECT count(*) FROM event_participants WHERE event_id = $1 AND user_id = $2 AND invited`,
			f.eventID,
			wrong,
		),
		"a refused change keeps the invitation",
	)

	_, err = f.uc.ChangeParticipantInvitationEmail(
		ctx,
		f.eventID,
		captain,
		f.ownerID,
		"other@tools.test",
		true,
	)
	require.ErrorIs(
		t,
		err,
		participantModel.ErrInvitationRequired.Err(),
		"an accepted participant is not an invitation",
	)

	_, err = f.uc.ChangeParticipantInvitationEmail(
		ctx,
		f.eventID,
		uuid.Must(uuid.NewV7()),
		f.ownerID,
		"other@tools.test",
		true,
	)
	require.ErrorIs(t, err, participantModel.ErrInvitationRequired.Err())
}

func TestTeamInvitationsSayWhetherTheyAwaitPublicationOrExpired(t *testing.T) {
	f := newToolsFixture(t)
	ctx := context.Background()
	teamID, _ := f.newTeam(t, "Alpha")
	_, err := f.uc.InviteTeamMembers(
		ctx,
		f.eventID,
		teamID,
		f.ownerID,
		[]event.ParticipantInvitationInput{{Email: "wait@tools.test"}},
		true,
	)
	require.NoError(t, err)
	invitation := func() event.TeamInvitationView {
		res, listErr := f.uc.ListTeams(ctx, event.ListTeamsFilter{EventID: f.eventID, PageSize: 50})
		require.NoError(t, listErr)
		for _, team := range res.Teams {
			if team.ID == teamID {
				require.Len(t, team.PendingInvitations, 1)
				return team.PendingInvitations[0]
			}
		}
		t.Fatal("team is not listed")
		return event.TeamInvitationView{}
	}

	got := invitation()
	require.False(t, got.InvitationExpired)
	require.False(t, got.InvitationAwaitingPublication, "a published event with an open roster")

	// not published yet: the invitation waits for the publication, it is not «expired»
	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE events SET publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5 WHERE id = $1`,
		f.eventID,
		time.Now().Add(time.Hour),
		time.Now().Add(2*time.Hour),
		time.Now().Add(4*time.Hour),
		time.Now().Add(6*time.Hour),
	)
	require.NoError(t, err)
	got = invitation()
	require.True(t, got.InvitationAwaitingPublication)
	require.False(t, got.InvitationExpired)

	// the event is over: expired
	f.shiftLifecycle(t, -3*time.Hour, -time.Hour)
	got = invitation()
	require.True(t, got.InvitationExpired)
	require.False(t, got.InvitationAwaitingPublication)
}
