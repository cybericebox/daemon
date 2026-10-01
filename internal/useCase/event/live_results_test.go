package event_test

import (
	"context"
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
