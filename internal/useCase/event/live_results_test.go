package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestReplayLiveResultsReturnsDeltasAfterFreshSnapshot(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 6, UpdatedAt: now}, nil)
	q.EXPECT().GetEarliestEventResultChangeRevision(gomock.Any(), eventID).Return(int64(5), nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().ListEventResultChangesAfter(gomock.Any(), postgres.ListEventResultChangesAfterParams{EventID: eventID, AfterRevision: 4, LimitVal: 500}).Return([]postgres.EventResultChange{{EventID: eventID, Revision: 5, Kind: "team_challenge_solved", Payload: []byte(`{"TeamID":"blue"}`), CreatedAt: now}}, nil)

	replay, err := uc.ReplayLiveResults(context.Background(), eventID, event.ResultsAccess{}, 4, false)
	if err != nil || replay.SnapshotRequired || len(replay.Changes) != 1 || replay.Changes[0].Revision != 5 {
		t.Fatalf("ReplayLiveResults = %#v, %v", replay, err)
	}
}

func TestReplayLiveResultsRequestsSnapshotForExpiredCursor(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 6, UpdatedAt: now}, nil)
	q.EXPECT().GetEarliestEventResultChangeRevision(gomock.Any(), eventID).Return(int64(4), nil)

	replay, err := uc.ReplayLiveResults(context.Background(), eventID, event.ResultsAccess{}, 1, false)
	if err != nil || !replay.SnapshotRequired || len(replay.Changes) != 0 {
		t.Fatalf("ReplayLiveResults = %#v, %v", replay, err)
	}
}

func TestCleanupExpiredResultChangesUsesShortLiveRetention(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	var cutoff time.Time
	q.EXPECT().DeleteEventResultChangesBefore(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, before time.Time) (int64, error) {
		cutoff = before
		return 0, nil
	})

	startedAt := time.Now().UTC()
	if err := uc.CleanupExpiredResultChanges(context.Background()); err != nil {
		t.Fatalf("CleanupExpiredResultChanges: %v", err)
	}
	if cutoff.Before(startedAt.Add(-16*time.Minute)) || cutoff.After(time.Now().UTC().Add(-14*time.Minute)) {
		t.Fatalf("cleanup cutoff = %s, want approximately 15 minutes ago", cutoff)
	}
}

func replayWith(t *testing.T, rows int32, scoreboard []postgres.ListEventScoreboardRow, changeTeams ...uuid.UUID) event.LiveResultsReplay {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	cfg := postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}
	if rows > 0 {
		cfg.ResultsFreezeMinutes, cfg.ResultsChartTeams = 90, 1
		cfg.ResultsRowsLimit = pgtype.Int4{Int32: rows, Valid: true}
	}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(cfg, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: int64(4 + len(changeTeams)), UpdatedAt: now}, nil)
	q.EXPECT().GetEarliestEventResultChangeRevision(gomock.Any(), eventID).Return(int64(5), nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return(scoreboard, nil).AnyTimes()
	var rowsOut []postgres.EventResultChange
	for i, team := range changeTeams {
		payload, _ := json.Marshal(map[string]any{"TeamID": team, "EventChallengeID": uuid.Must(uuid.NewV7()), "SolvedAt": now})
		rowsOut = append(rowsOut, postgres.EventResultChange{EventID: eventID, Revision: int64(5 + i), Kind: "team_challenge_solved", Payload: payload, CreatedAt: now})
	}
	q.EXPECT().ListEventResultChangesAfter(gomock.Any(), gomock.Any()).Return(rowsOut, nil)
	replay, err := uc.ReplayLiveResults(context.Background(), eventID, event.ResultsAccess{}, 4, false)
	if err != nil {
		t.Fatal(err)
	}
	return replay
}

// The reported leaks: a verdict on a hidden team, and teams below the row limit, reached the public live stream.
func TestLiveDeltasOfAHiddenTeamNeverReachAPublicViewer(t *testing.T) {
	shown, hidden := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	replay := replayWith(t, 0, []postgres.ListEventScoreboardRow{{TeamID: shown, TeamName: "Blue"}}, hidden, shown)
	if len(replay.Changes) != 1 || replay.Changes[0].Revision != 6 {
		t.Fatalf("only the visible team's change may be sent: %+v", replay.Changes)
	}
}

func TestLiveDeltasBelowTheRowLimitBecomeAReload(t *testing.T) {
	top, low := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	board := []postgres.ListEventScoreboardRow{{TeamID: top, TeamName: "Top", Points: 100}, {TeamID: low, TeamName: "Low", Points: 10}}
	replay := replayWith(t, 1, board, low, top)
	kinds := make([]string, 0, len(replay.Changes))
	for _, c := range replay.Changes {
		kinds = append(kinds, c.Kind)
	}
	if len(kinds) != 2 || kinds[0] != "team_challenge_solved" || kinds[1] != "scoreboard_recalculated" {
		t.Fatalf("the team below the limit is not shown, the client reloads instead: %v", kinds)
	}
	if replay.Changes[0].Revision != 6 {
		t.Fatalf("the top team's delta is kept: %+v", replay.Changes[0])
	}
	// Without a row limit every visible team's delta is sent and no reload is forced.
	if replay = replayWith(t, 0, board, low, top); len(replay.Changes) != 2 || replay.Changes[1].Kind != "team_challenge_solved" {
		t.Fatalf("no limit: %+v", replay.Changes)
	}
}
