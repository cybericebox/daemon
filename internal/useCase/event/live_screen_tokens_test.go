package event_test

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
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
	q.EXPECT().IssueEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.IssueEventLiveScreenLinkParams) error {
		stored = append(stored, p.TokenHash)
		return nil
	})
	issued, err := uc.IssueLiveScreenLink(ctx, eventID, by, eventContentModel.LiveScreenExpiryNone)
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
	if err = uc.RevokeLiveScreenLink(ctx, eventID); err != nil {
		t.Fatalf("RevokeLiveScreenLink: %v", err)
	}
	if uc.IsActiveLiveScreenToken(ctx, issued.Token, now) {
		t.Fatal("a revoked link must lose its own budget at once")
	}
}

// A failed read keeps the last known set.
func TestFailedReadKeepsTheKnownLinks(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	working := randomScreenToken(t)
	gomock.InOrder(
		q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).Return([]postgres.ListActiveLiveScreenTokenHashesRow{{TokenHash: eventContentModel.HashLiveScreenToken(working)}}, nil),
		q.EXPECT().ListActiveLiveScreenTokenHashes(gomock.Any(), gomock.Any()).Return(nil, errors.New("database is down")),
	)
	ctx := context.Background()
	if !uc.IsActiveLiveScreenToken(ctx, working, now) || !uc.IsActiveLiveScreenToken(ctx, working, now.Add(31*time.Second)) {
		t.Fatal("the working link stays known through a failed read")
	}
}
