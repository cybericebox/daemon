package event_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func liveScreenUseCase(t *testing.T) (*event.EventUseCase, *postgresMocks.MockQuerier) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	return event.NewEventUseCase(event.Dependencies{Repo: q}), q
}

func TestIssueLiveScreenLinkStoresOnlyTheHash(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID, by := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	started := startedEvent(eventID, time.Now().UTC())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
	var written postgres.IssueEventLiveScreenLinkParams
	q.EXPECT().IssueEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.IssueEventLiveScreenLinkParams) error {
		written = p
		return nil
	})

	got, err := uc.IssueLiveScreenLink(context.Background(), eventID, by, eventContentModel.LiveScreenExpiryEventEnd)
	if err != nil {
		t.Fatalf("IssueLiveScreenLink: %v", err)
	}
	if !bytes.Equal(written.TokenHash, eventContentModel.HashLiveScreenToken(got.Token)) || bytes.Contains(written.TokenHash, []byte(got.Token)) {
		t.Fatal("only the token hash may be written")
	}
	if written.EventID != eventID || written.CreatedBy.UUID != by || !written.ExpiresAt.Valid || !written.ExpiresAt.Time.Equal(started.FinishAt.Time) {
		t.Fatalf("written = %+v", written)
	}
}

func TestIssueLiveScreenLinkWithoutExpiry(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now().UTC()), nil)
	q.EXPECT().IssueEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.IssueEventLiveScreenLinkParams) error {
		if p.ExpiresAt.Valid {
			t.Fatalf("«Без обмеження» wrote an expiry: %+v", p.ExpiresAt)
		}
		return nil
	})
	if got, err := uc.IssueLiveScreenLink(context.Background(), eventID, uuid.Nil, eventContentModel.LiveScreenExpiryNone); err != nil || got.ExpiresAt != nil {
		t.Fatalf("no expiry = %+v, %v", got, err)
	}
}

func TestRegenerateLiveScreenLinkKeepsTheExpiry(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID, oldID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expires := time.Now().UTC().Add(48 * time.Hour)
	q.EXPECT().GetActiveEventLiveScreenLink(gomock.Any(), gomock.Any()).
		Return(postgres.EventLiveScreenLink{ID: oldID, EventID: eventID, TokenHash: bytes.Repeat([]byte{1}, 32), ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}, nil)
	q.EXPECT().IssueEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.IssueEventLiveScreenLinkParams) error {
		if p.ID == oldID || p.EventID != eventID || !p.ExpiresAt.Time.Equal(expires) {
			t.Fatalf("issue = %+v", p)
		}
		return nil
	})
	got, err := uc.RegenerateLiveScreenLink(context.Background(), eventID, uuid.Nil)
	if err != nil || got.Token == "" || got.ID == oldID || got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("regenerated = %+v, %v", got, err)
	}
}

func TestRegenerateWithoutALinkIsNotFound(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	q.EXPECT().GetActiveEventLiveScreenLink(gomock.Any(), gomock.Any()).Return(postgres.EventLiveScreenLink{}, pgx.ErrNoRows)
	if _, err := uc.RegenerateLiveScreenLink(context.Background(), uuid.Must(uuid.NewV7()), uuid.Nil); !errors.Is(err, eventContentModel.ErrLiveScreenLinkNotFound.Err()) {
		t.Fatalf("regenerate without a link = %v", err)
	}
}

func TestGetAndRevokeLiveScreenLink(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetActiveEventLiveScreenLink(gomock.Any(), gomock.Any()).Return(postgres.EventLiveScreenLink{}, pgx.ErrNoRows)
	if link, err := uc.GetLiveScreenLink(context.Background(), eventID); err != nil || link != nil {
		t.Fatalf("no link = %+v, %v", link, err)
	}
	q.EXPECT().RevokeEventLiveScreenLinks(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	if err := uc.RevokeLiveScreenLink(context.Background(), eventID); err != nil {
		t.Fatalf("turning off without a link = %v", err)
	}
}

func TestResolveLiveScreenToken(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	link, token, _ := eventContentModel.NewLiveScreenLink(eventID, uuid.Nil, now, eventContentModel.LiveScreenExpiryDay, nil)
	row := postgres.EventLiveScreenLink{ID: link.ID, EventID: eventID, TokenHash: link.TokenHash, CreatedAt: now, ExpiresAt: pgtype.Timestamptz{Time: *link.ExpiresAt, Valid: true}}
	for name, tc := range map[string]struct {
		row     postgres.EventLiveScreenLink
		lookup  error
		eventID uuid.UUID
		ok      bool
	}{
		"valid":       {row: row, eventID: eventID, ok: true},
		"no expiry":   {row: func() postgres.EventLiveScreenLink { r := row; r.ExpiresAt = pgtype.Timestamptz{}; return r }(), eventID: eventID, ok: true},
		"other event": {row: row, eventID: uuid.Must(uuid.NewV7())},
		"unknown":     {lookup: pgx.ErrNoRows, eventID: eventID},
		"expired": {row: func() postgres.EventLiveScreenLink {
			r := row
			r.ExpiresAt = pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}
			return r
		}(), eventID: eventID},
		"revoked": {row: func() postgres.EventLiveScreenLink {
			r := row
			r.RevokedAt = pgtype.Timestamptz{Time: now, Valid: true}
			return r
		}(), eventID: eventID},
	} {
		t.Run(name, func(t *testing.T) {
			uc, q := liveScreenUseCase(t)
			q.EXPECT().GetEventLiveScreenLinkByTokenHash(gomock.Any(), link.TokenHash).Return(tc.row, tc.lookup)
			err := uc.ResolveLiveScreenToken(context.Background(), tc.eventID, token)
			if tc.ok != (err == nil) || !tc.ok && !errors.Is(err, eventContentModel.ErrLiveScreenTokenInvalid.Err()) {
				t.Fatalf("resolve = %v", err)
			}
		})
	}
	uc, _ := liveScreenUseCase(t)
	if err := uc.ResolveLiveScreenToken(context.Background(), eventID, "not-a-token"); !errors.Is(err, eventContentModel.ErrLiveScreenTokenInvalid.Err()) {
		t.Fatalf("malformed token = %v", err)
	}
}
