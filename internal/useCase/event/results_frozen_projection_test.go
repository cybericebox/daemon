package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// Neither the snapshot token nor polling/SSE may acknowledge hidden activity,
// even if the hidden activity exceeds the retained change window.
func TestFrozenResultsVersionDescribesOnlyVisibleProjection(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	clock := newFakeClock(uc)
	eventID, team := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	started := startedEvent(eventID, now)
	cutoff := pgtype.Timestamptz{Time: started.FinishAt.Time.Add(-90 * time.Minute), Valid: true}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 10), nil).Times(3)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil).Times(3)
	gomock.InOrder(
		q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 4, UpdatedAt: now}, nil),
		q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 1004, UpdatedAt: now.Add(time.Minute)}, nil),
		q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 1005, UpdatedAt: now.Add(2 * time.Minute)}, nil),
	)
	hidden := make([]postgres.EventResultChange, 500)
	for i := range hidden {
		hidden[i] = solveChange(eventID, int64(i+5), team, now)
	}
	q.EXPECT().ListEventResultChangesAfter(gomock.Any(), gomock.Any()).Return(hidden, nil).Times(2)
	params := postgres.ListEventScoreboardParams{EventID: eventID, Cutoff: cutoff}
	gomock.InOrder(
		q.EXPECT().ListEventScoreboard(gomock.Any(), params).Return([]postgres.ListEventScoreboardRow{{TeamID: team, TeamName: "Blue", Points: 100, Solved: 1}}, nil),
		// A private last-solve timestamp is not part of the public version.
		q.EXPECT().ListEventScoreboard(gomock.Any(), params).Return([]postgres.ListEventScoreboardRow{{TeamID: team, TeamName: "Blue", Points: 100, Solved: 1, LastSolveAt: pgtype.Timestamptz{Time: now, Valid: true}}}, nil),
		q.EXPECT().ListEventScoreboard(gomock.Any(), params).Return([]postgres.ListEventScoreboardRow{{TeamID: team, TeamName: "Renamed", Points: 100, Solved: 1}}, nil),
	)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID, Cutoff: cutoff}).Return(nil, nil).Times(3)
	ctx := context.Background()
	first, err := uc.GetResultsSnapshot(ctx, eventID, event.ResultsAccess{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision <= 0 || first.Revision > 1<<53-1 {
		t.Fatalf("version must be an exact positive JavaScript integer: %d", first.Revision)
	}
	stream := uc.OpenLiveResults(eventID, event.ResultsAccess{}, false)
	clock.advance(2 * time.Second)
	second, err := uc.GetResultsSnapshot(ctx, eventID, event.ResultsAccess{}, false)
	if err != nil || second.Revision != first.Revision {
		t.Fatalf("hidden changes altered frozen version: %d -> %d, %v", first.Revision, second.Revision, err)
	}
	replay, err := stream.Replay(ctx, first.Revision)
	if err != nil || replay.SnapshotRequired || len(replay.Changes) != 0 || replay.LastRevision != first.Revision || replay.FreezeKey == "" {
		t.Fatalf("hidden activity leaked through replay: %+v, %v", replay, err)
	}
	clock.advance(2 * time.Second)
	replay, err = stream.Replay(ctx, first.Revision)
	if err != nil || !replay.SnapshotRequired || len(replay.Changes) != 0 {
		t.Fatalf("a visible name change must reload without raw changes: %+v, %v", replay, err)
	}
}

// Personal progress is live even with popularity scoring; using the frozen
// population for own points would disagree with the live award timeline.
func TestPersonalResultsKeepFrozenRankAndFullyLivePoints(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, userID, own, leader := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	started := startedEvent(eventID, now)
	cutoff := pgtype.Timestamptz{Time: started.FinishAt.Time.Add(-90 * time.Minute), Valid: true}
	ownParam := uuid.NullUUID{UUID: own, Valid: true}
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: ownParam, CreatedAt: now}, nil).Times(3)
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 10), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID, Cutoff: cutoff}).Return([]postgres.ListEventScoreboardRow{{TeamID: leader, Points: 200}, {TeamID: own, Points: 100, Solved: 1}}, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID, IncludeTeam: ownParam}).Return([]postgres.ListEventScoreboardRow{{TeamID: own, Points: 50, Solved: 1}}, nil)
	q.EXPECT().ListTeamScoreTimeline(gomock.Any(), own).Return([]postgres.ListTeamScoreTimelineRow{{Points: 50, SolvedAt: now.Add(-time.Hour)}}, nil)
	got, err := uc.GetOwnResults(context.Background(), eventID, userID)
	if err != nil || got.Entry.Rank != 2 || got.Entry.Points != 50 || len(got.Timeline) != 1 || got.Timeline[0].Points != 50 {
		t.Fatalf("personal results = %+v, %v", got, err)
	}
}
