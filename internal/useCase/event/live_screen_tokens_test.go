package event_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func randomScreenToken(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestIsActiveLiveScreenTokenKnowsOnlyWorkingLinks(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	working, expiring := randomScreenToken(t), randomScreenToken(t)
	q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).Return([]postgres.ListActiveLiveScreenTokenHashesRow{
		{TokenHash: eventContentModel.HashLiveScreenToken(working)},
		{TokenHash: eventContentModel.HashLiveScreenToken(expiring), ExpiresAt: pgtype.Timestamptz{Time: now.Add(2 * time.Second), Valid: true}},
	}, nil).Times(1)
	ctx := context.Background()
	if !uc.IsActiveLiveScreenToken(ctx, working, now) || !uc.IsActiveLiveScreenToken(ctx, expiring, now) {
		t.Fatal("working links must be known")
	}
	if uc.IsActiveLiveScreenToken(ctx, "not-a-token", now) {
		t.Fatal("a malformed token is never a link")
	}
	// Expired between two reads: no longer a link, without another read.
	if uc.IsActiveLiveScreenToken(ctx, expiring, now.Add(3*time.Second)) {
		t.Fatal("an expired link must not keep its own budget")
	}
}

// A flood of made-up tokens costs at most one read per reload interval.
func TestUnknownTokensReloadTheSetAtMostEveryFiveSeconds(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).Return(nil, nil).Times(2)
	ctx := context.Background()
	for i := 0; i < 2000; i++ {
		if uc.IsActiveLiveScreenToken(ctx, randomScreenToken(t), now.Add(time.Duration(i)*time.Millisecond)) {
			t.Fatal("a random token is no link")
		}
	}
	uc.IsActiveLiveScreenToken(ctx, randomScreenToken(t), now.Add(5*time.Second))
}

// A link issued on this instance is known at once; a revoked one is gone at once.
func TestIssueAndRevokeRefreshTheKnownLinks(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	ctx := context.Background()
	eventID, by := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var stored [][]byte
	q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, pgtype.Timestamptz) ([]postgres.ListActiveLiveScreenTokenHashesRow, error) {
		rows := make([]postgres.ListActiveLiveScreenTokenHashesRow, 0, len(stored))
		for _, hash := range stored {
			rows = append(rows, postgres.ListActiveLiveScreenTokenHashesRow{TokenHash: hash})
		}
		return rows, nil
	}).AnyTimes()
	now := time.Now()
	if uc.IsActiveLiveScreenToken(ctx, randomScreenToken(t), now) {
		t.Fatal("no links yet")
	}
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	screenID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventLiveScreen(gomock.Any(), gomock.Any()).Return(liveScreenRow(t, eventID, screenID, "Екран 1"), nil).Times(2)
	q.EXPECT().IssueEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.IssueEventLiveScreenLinkParams) error {
		stored = append(stored, p.TokenHash)
		return nil
	})
	issued, err := uc.IssueLiveScreenLink(ctx, eventID, screenID, by, eventContentModel.LiveScreenExpiryNone)
	if err != nil {
		t.Fatalf("IssueLiveScreenLink: %v", err)
	}
	if !uc.IsActiveLiveScreenToken(ctx, issued.Token, now) {
		t.Fatal("a link issued here must be known at once")
	}
	q.EXPECT().RevokeEventLiveScreenLinks(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, postgres.RevokeEventLiveScreenLinksParams) (int64, error) {
		stored = nil
		return 1, nil
	})
	if err = uc.RevokeLiveScreenLink(ctx, eventID, screenID); err != nil {
		t.Fatalf("RevokeLiveScreenLink: %v", err)
	}
	if uc.IsActiveLiveScreenToken(ctx, issued.Token, now) {
		t.Fatal("a revoked link must lose its own budget at once")
	}
}

// A failed read keeps the last known set, does not count as fresh, and the
// next attempt comes after the attempt bound.
func TestFailedReadKeepsTheKnownLinksAndRetriesAfterTheBound(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	working, later := randomScreenToken(t), randomScreenToken(t)
	rows := func(tokens ...string) []postgres.ListActiveLiveScreenTokenHashesRow {
		out := make([]postgres.ListActiveLiveScreenTokenHashesRow, 0, len(tokens))
		for _, token := range tokens {
			out = append(out, postgres.ListActiveLiveScreenTokenHashesRow{TokenHash: eventContentModel.HashLiveScreenToken(token)})
		}
		return out
	}
	gomock.InOrder(
		q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).Return(rows(working), nil),
		q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).Return(nil, errors.New("database is down")),
		q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).Return(rows(working, later), nil),
	)
	ctx := context.Background()
	if !uc.IsActiveLiveScreenToken(ctx, working, now) {
		t.Fatal("the working link must be known")
	}
	// Past the TTL the read fails: the old set stays.
	if !uc.IsActiveLiveScreenToken(ctx, working, now.Add(31*time.Second)) {
		t.Fatal("the working link stays known through a failed read")
	}
	// Within the attempt bound no new read happens.
	if uc.IsActiveLiveScreenToken(ctx, later, now.Add(33*time.Second)) {
		t.Fatal("no read yet within the attempt bound")
	}
	// After the bound the set is read again: the failure did not count as fresh.
	if !uc.IsActiveLiveScreenToken(ctx, later, now.Add(37*time.Second)) {
		t.Fatal("the next attempt after the bound must load the new link")
	}
}

// A request that is cancelled does not spoil the read it triggered.
func TestCancelledRequestDoesNotPoisonTheKnownLinks(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	working := randomScreenToken(t)
	q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, _ pgtype.Timestamptz) ([]postgres.ListActiveLiveScreenTokenHashesRow, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return []postgres.ListActiveLiveScreenTokenHashesRow{{TokenHash: eventContentModel.HashLiveScreenToken(working)}}, nil
	}).Times(1)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if !uc.IsActiveLiveScreenToken(cancelled, working, now) {
		t.Fatal("a cancelled request must still load the set")
	}
	if !uc.IsActiveLiveScreenToken(context.Background(), working, now.Add(time.Second)) {
		t.Fatal("the loaded set serves the next request")
	}
}

// Concurrent requests with an empty cache share one database read.
func TestConcurrentMissesShareOneRead(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	working := randomScreenToken(t)
	release := make(chan struct{})
	q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, pgtype.Timestamptz) ([]postgres.ListActiveLiveScreenTokenHashesRow, error) {
		<-release
		return []postgres.ListActiveLiveScreenTokenHashesRow{{TokenHash: eventContentModel.HashLiveScreenToken(working)}}, nil
	}).Times(1)
	var wg sync.WaitGroup
	results := make(chan bool, 40)
	for i := 0; i < 40; i++ {
		token := working
		if i%2 == 1 {
			token = randomScreenToken(t)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- uc.IsActiveLiveScreenToken(context.Background(), token, now) == (token == working)
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	for ok := range results {
		if !ok {
			t.Fatal("a request got the wrong answer")
		}
	}
}
