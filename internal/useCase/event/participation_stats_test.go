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
