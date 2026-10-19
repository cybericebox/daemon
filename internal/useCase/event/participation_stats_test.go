package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// The participation read is derived from the caller's own participant row:
// every team query is asked for that team and no other.
func TestGetParticipationStatsReadsOnlyTheCallersTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	participant := postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, CreatedAt: now, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(participant, nil).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).AnyTimes()
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 3, UpdatedAt: now}, nil).AnyTimes()
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return([]postgres.ListEventScoreboardRow{
		{TeamID: uuid.Must(uuid.NewV7()), TeamName: "Other", Points: 900},
		{TeamID: teamID, TeamName: "Own", Points: 100, Solved: 1},
	}, nil).AnyTimes()
	q.EXPECT().ListTeamScoreTimeline(gomock.Any(), teamID).Return([]postgres.ListTeamScoreTimelineRow{{Points: 100, SolvedAt: now}}, nil)
	q.EXPECT().ListParticipationSolves(gomock.Any(), postgres.ListParticipationSolvesParams{EventID: eventID, EventTeamID: teamID}).Return([]postgres.ListParticipationSolvesRow{
		{ChallengeName: "Web 1", Category: "Web", Points: 100, SolvedAt: now, SolvedBy: userID, SolvedByName: "Me", FirstBlood: true},
	}, nil)
	q.EXPECT().ListParticipationMembers(gomock.Any(), postgres.ListParticipationMembersParams{EventID: eventID, EventTeamID: teamID}).Return([]postgres.ListParticipationMembersRow{
		{UserID: userID, DisplayName: "Me", JoinedAt: now, Attempts: 3, CorrectAttempts: 1, Hints: 1},
		{UserID: uuid.Must(uuid.NewV7()), DisplayName: "Mate", JoinedAt: now},
	}, nil)
	q.EXPECT().GetParticipationTeamTotals(gomock.Any(), postgres.GetParticipationTeamTotalsParams{EventID: eventID, EventTeamID: teamID}).Return(postgres.GetParticipationTeamTotalsRow{Attempts: 5, CorrectAttempts: 1, Hints: 1}, nil)

	got, err := uc.GetParticipationStats(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("GetParticipationStats: %v", err)
	}
	if got.Rank != 2 || got.Points != 100 || got.Team.TeamName != "Own" || len(got.Team.Solves) != 1 || got.Team.FirstBloods != 1 {
		t.Fatalf("stats = %+v", got)
	}
	if got.Me.UserID != userID || got.Me.Points != 100 || got.Me.Solves != 1 || got.Me.FirstBloods != 1 || got.Me.Attempts != 3 {
		t.Fatalf("me = %+v", got.Me)
	}
	if got.Team.Attempts != 5 || len(got.Team.Members) != 2 || got.Team.Members[1].Points != 0 {
		t.Fatalf("team = %+v", got.Team)
	}
}

// A user without an approved team place has no participation data at all.
func TestGetParticipationStatsRejectsUserWithoutTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, CreatedAt: time.Now()}, nil).AnyTimes()

	_, err := uc.GetParticipationStats(context.Background(), eventID, userID)
	if !errors.Is(err, participantModel.ErrParticipantNotApproved.Err()) {
		t.Fatalf("err = %v, want ErrParticipantNotApproved", err)
	}
}

// The rank is not disclosed while the results are hidden from the participant.
func TestGetParticipationStatsHidesRankWhenResultsAreHidden(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, CreatedAt: now, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityHidden), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).AnyTimes()
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil).AnyTimes()
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return([]postgres.ListEventScoreboardRow{{TeamID: teamID, TeamName: "Own"}}, nil).AnyTimes()
	q.EXPECT().ListTeamScoreTimeline(gomock.Any(), teamID).Return(nil, nil)
	q.EXPECT().ListParticipationSolves(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().ListParticipationMembers(gomock.Any(), gomock.Any()).Return(nil, nil)
	q.EXPECT().GetParticipationTeamTotals(gomock.Any(), gomock.Any()).Return(postgres.GetParticipationTeamTotalsRow{}, nil)

	got, err := uc.GetParticipationStats(context.Background(), eventID, userID)
	if err != nil || got.Rank != 0 {
		t.Fatalf("stats = %+v, %v", got, err)
	}
}

// Other members' account ids serve only the captain: a member gets them blanked in the roster and the solves,
// keeps their own, and the per-team cache shared by both stays intact.
func TestGetParticipationStatsGivesOtherMembersIDsOnlyToTheCaptain(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, captainID, memberID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	roles := map[uuid.UUID]participantModel.TeamRole{captainID: participantModel.TeamRoleCaptain, memberID: participantModel.TeamRoleMember}
	for userID, role := range roles {
		q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
			EventID: eventID, UserID: userID, Status: 2, CreatedAt: now, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, TeamRole: pgtype.Int2{Int16: int16(role), Valid: true},
		}, nil).AnyTimes()
	}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil).AnyTimes()
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).AnyTimes()
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 3, UpdatedAt: now}, nil).AnyTimes()
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return([]postgres.ListEventScoreboardRow{{TeamID: teamID, TeamName: "Own", Points: 300, Solved: 2}}, nil).AnyTimes()
	q.EXPECT().ListTeamScoreTimeline(gomock.Any(), teamID).Return(nil, nil).AnyTimes()
	q.EXPECT().ListParticipationSolves(gomock.Any(), gomock.Any()).Return([]postgres.ListParticipationSolvesRow{
		{ChallengeName: "Web 1", Points: 100, SolvedAt: now, SolvedBy: captainID, SolvedByName: "Captain"},
		{ChallengeName: "Web 2", Points: 200, SolvedAt: now, SolvedBy: memberID, SolvedByName: "Member"},
	}, nil).AnyTimes()
	q.EXPECT().ListParticipationMembers(gomock.Any(), gomock.Any()).Return([]postgres.ListParticipationMembersRow{
		{UserID: captainID, DisplayName: "Captain", JoinedAt: now},
		{UserID: memberID, DisplayName: "Member", JoinedAt: now},
	}, nil).AnyTimes()
	q.EXPECT().GetParticipationTeamTotals(gomock.Any(), gomock.Any()).Return(postgres.GetParticipationTeamTotalsRow{}, nil).AnyTimes()

	member, err := uc.GetParticipationStats(context.Background(), eventID, memberID)
	if err != nil {
		t.Fatalf("member: %v", err)
	}
	if member.Me.UserID != memberID || member.Me.Points != 200 {
		t.Fatalf("member keeps their own share: %+v", member.Me)
	}
	if member.Team.Members[0].UserID != uuid.Nil || member.Team.Members[0].Name != "Captain" || member.Team.Members[0].Points != 100 || member.Team.Members[1].UserID != memberID {
		t.Fatalf("member roster: %+v", member.Team.Members)
	}
	if member.Team.Solves[0].SolvedByUserID != uuid.Nil || member.Team.Solves[0].SolvedByName != "Captain" || member.Team.Solves[1].SolvedByUserID != memberID {
		t.Fatalf("member solves: %+v", member.Team.Solves)
	}
	captain, err := uc.GetParticipationStats(context.Background(), eventID, captainID)
	if err != nil {
		t.Fatalf("captain: %v", err)
	}
	if captain.Team.Members[1].UserID != memberID || captain.Team.Solves[1].SolvedByUserID != memberID {
		t.Fatalf("captain sees the ids: %+v %+v", captain.Team.Members, captain.Team.Solves)
	}
}
