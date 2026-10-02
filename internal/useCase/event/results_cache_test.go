package event_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

const viewers = 200

// fakeClock is the results cache clock, moved by the test between ticks.
type fakeClock struct{ nanos atomic.Int64 }

func newFakeClock(uc *event.EventUseCase) *fakeClock {
	c := &fakeClock{}
	c.nanos.Store(time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC).UnixNano())
	event.SetResultsCacheClock(uc, func() time.Time { return time.Unix(0, c.nanos.Load()) })
	return c
}

func (c *fakeClock) advance(d time.Duration) { c.nanos.Add(int64(d)) }

// concurrently runs fn for every viewer at once.
func concurrently(t *testing.T, fn func(i int) error) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make(chan error, viewers)
	start := make(chan struct{})
	for i := range viewers {
		wg.Go(func() {
			<-start
			if err := fn(i); err != nil {
				errs <- err
			}
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

func publicConfig(eventID uuid.UUID, now time.Time) postgres.EventConfig {
	return postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}
}

func solveChange(eventID uuid.UUID, revision int64, team uuid.UUID, at time.Time) postgres.EventResultChange {
	payload, _ := json.Marshal(map[string]any{"TeamID": team, "EventChallengeID": uuid.Must(uuid.NewV7()), "SolvedAt": at})
	return postgres.EventResultChange{EventID: eventID, Revision: revision, Kind: "team_challenge_solved", Payload: payload, CreatedAt: at}
}

// 200 open streams of one event cost one read of each shared query per tick,
// not 200: every tick fans one read out to all subscribers.
func TestLiveResultsStreamsShareOneReadPerTick(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	clock := newFakeClock(uc)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(publicConfig(eventID, now), nil).Times(2)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).Times(2)
	team := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return([]postgres.ListEventScoreboardRow{{TeamID: team, TeamName: "Blue"}}, nil).AnyTimes()
	gomock.InOrder(
		q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 6, UpdatedAt: now}, nil),
		q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 7, UpdatedAt: now}, nil),
	)
	q.EXPECT().ListEventResultChangesAfter(gomock.Any(), postgres.ListEventResultChangesAfterParams{EventID: eventID, AfterRevision: 6, LimitVal: 500}).
		Return([]postgres.EventResultChange{solveChange(eventID, 7, team, now)}, nil).Times(1)

	streams := make([]event.LiveResultsSubscription, viewers)
	for i := range streams {
		streams[i] = uc.OpenLiveResults(eventID, event.ResultsAccess{}, false)
	}
	concurrently(t, func(i int) error {
		replay, err := streams[i].Replay(context.Background(), 6)
		if err != nil || replay.SnapshotRequired || len(replay.Changes) != 0 || replay.LastRevision != 6 {
			t.Errorf("tick 1: replay = %+v, %v", replay, err)
		}
		return nil
	})
	clock.advance(2 * time.Second)
	concurrently(t, func(i int) error {
		replay, err := streams[i].Replay(context.Background(), 6)
		if err != nil || replay.SnapshotRequired || len(replay.Changes) != 1 || replay.Changes[0].Revision != 7 || replay.LastRevision != 7 {
			t.Errorf("tick 2: replay = %+v, %v", replay, err)
		}
		return nil
	})
}

// A stream reads its viewer once, not on every tick.
func TestLiveResultsStreamReadsViewerOnce(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	clock := newFakeClock(uc)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(publicConfig(eventID, now), nil).Times(3)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).Times(3)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 6, UpdatedAt: now}, nil).Times(3)
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows).Times(1)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows).Times(1)

	stream := uc.OpenLiveResults(eventID, event.ResultsAccess{UserID: &userID}, false)
	for range 3 {
		if replay, err := stream.Replay(context.Background(), 6); err != nil || replay.SnapshotRequired {
			t.Fatalf("replay = %+v, %v", replay, err)
		}
		clock.advance(2 * time.Second)
	}
}

// The shared window keeps the freeze exact: a frozen viewer still skips other
// teams' solves after the freeze and gets its own team's.
func TestLiveResultsSharedWindowKeepsFreeze(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	clock := newFakeClock(uc)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	started := startedEvent(eventID, now)
	frozenAt := started.FinishAt.Time.Add(-90 * time.Minute)
	team := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return([]postgres.ListEventScoreboardRow{{TeamID: team, TeamName: "Blue"}}, nil).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 10), nil).Times(2)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil).Times(2)
	gomock.InOrder(
		q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 4, UpdatedAt: now}, nil),
		q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 6, UpdatedAt: now}, nil),
	)
	q.EXPECT().ListEventResultChangesAfter(gomock.Any(), postgres.ListEventResultChangesAfterParams{EventID: eventID, AfterRevision: 4, LimitVal: 500}).
		Return([]postgres.EventResultChange{solveChange(eventID, 5, team, frozenAt.Add(-time.Minute)), solveChange(eventID, 6, team, frozenAt.Add(time.Minute))}, nil)

	stream := uc.OpenLiveResults(eventID, event.ResultsAccess{}, false)
	if _, err := stream.Replay(context.Background(), 4); err != nil {
		t.Fatalf("first replay: %v", err)
	}
	clock.advance(2 * time.Second)
	replay, err := stream.Replay(context.Background(), 4)
	if err != nil || len(replay.Changes) != 1 || replay.Changes[0].Revision != 5 || replay.LastRevision != 6 || replay.FreezeKey == "" {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
}

// 200 concurrent snapshots of one revision share one read of each query.
func TestResultsSnapshotsShareOneRead(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	newFakeClock(uc)
	eventID, team := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(publicConfig(eventID, now), nil).Times(1)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).Times(1)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 9, UpdatedAt: now}, nil).Times(1)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).
		Return([]postgres.ListEventScoreboardRow{{TeamID: team, TeamName: "Blue", Points: 100, Solved: 1}}, nil).Times(1)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID}).Return(nil, nil).Times(1)

	concurrently(t, func(int) error {
		got, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{}, false)
		if err != nil || got.Revision != 9 || len(got.Scoreboard) != 1 || got.Scoreboard[0].TeamID != team {
			t.Errorf("snapshot = %+v, %v", got, err)
		}
		return nil
	})
}
