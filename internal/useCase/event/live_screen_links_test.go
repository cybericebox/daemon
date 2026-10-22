package event_test

import (
	"bytes"
	"context"
	"encoding/json"
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

// liveScreenRow is a stored screen with the default layout.
func liveScreenRow(t *testing.T, eventID, screenID uuid.UUID, name string) postgres.EventLiveScreen {
	t.Helper()
	layout, err := json.Marshal(eventContentModel.DefaultLiveLayout())
	if err != nil {
		t.Fatal(err)
	}
	return postgres.EventLiveScreen{ID: screenID, EventID: eventID, Name: name, Layout: layout, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

// expectFirstScreen: the single-screen calls (uuid.Nil) resolve the first screen.
func expectFirstScreen(t *testing.T, q *postgresMocks.MockQuerier, eventID uuid.UUID) uuid.UUID {
	t.Helper()
	screenID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetFirstEventLiveScreen(gomock.Any(), eventID).Return(liveScreenRow(t, eventID, screenID, "Екран 1"), nil)
	return screenID
}

func TestIssueLiveScreenLinkStoresOnlyTheHash(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID, by := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	started := startedEvent(eventID, time.Now().UTC())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
	screenID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventLiveScreen(gomock.Any(), postgres.GetEventLiveScreenParams{ID: screenID, EventID: eventID}).Return(liveScreenRow(t, eventID, screenID, "Фойє"), nil)
	var written postgres.IssueEventLiveScreenLinkParams
	q.EXPECT().IssueEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.IssueEventLiveScreenLinkParams) error {
		written = p
		return nil
	})

	got, err := uc.IssueLiveScreenLink(context.Background(), eventID, screenID, by, eventContentModel.LiveScreenExpiryEventEnd)
	if err != nil {
		t.Fatalf("IssueLiveScreenLink: %v", err)
	}
	if !bytes.Equal(written.TokenHash, eventContentModel.HashLiveScreenToken(got.Token)) || bytes.Contains(written.TokenHash, []byte(got.Token)) {
		t.Fatal("only the token hash may be written")
	}
	if written.EventID != eventID || written.ScreenID != screenID || written.CreatedBy.UUID != by || !written.ExpiresAt.Valid || !written.ExpiresAt.Time.Equal(started.FinishAt.Time) {
		t.Fatalf("written = %+v", written)
	}
}

func TestIssueLiveScreenLinkWithoutExpiry(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now().UTC()), nil)
	first := expectFirstScreen(t, q, eventID)
	q.EXPECT().IssueEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.IssueEventLiveScreenLinkParams) error {
		if p.ScreenID != first {
			t.Fatalf("the single-screen route must issue the first screen's link: %+v", p)
		}
		if p.ExpiresAt.Valid {
			t.Fatalf("«Без обмеження» wrote an expiry: %+v", p.ExpiresAt)
		}
		return nil
	})
	if got, err := uc.IssueLiveScreenLink(context.Background(), eventID, uuid.Nil, uuid.Nil, eventContentModel.LiveScreenExpiryNone); err != nil || got.ExpiresAt != nil {
		t.Fatalf("no expiry = %+v, %v", got, err)
	}
}

func TestRegenerateLiveScreenLinkKeepsTheExpiry(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID, oldID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expires := time.Now().UTC().Add(48 * time.Hour)
	first := expectFirstScreen(t, q, eventID)
	q.EXPECT().GetActiveEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.GetActiveEventLiveScreenLinkParams) (postgres.EventLiveScreenLink, error) {
		if p.EventID != eventID || p.ScreenID != first {
			t.Fatalf("active link of %+v", p)
		}
		return postgres.EventLiveScreenLink{ID: oldID, EventID: eventID, ScreenID: first, TokenHash: bytes.Repeat([]byte{1}, 32), ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}}, nil
	})
	q.EXPECT().IssueEventLiveScreenLink(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.IssueEventLiveScreenLinkParams) error {
		if p.ID == oldID || p.EventID != eventID || p.ScreenID != first || !p.ExpiresAt.Time.Equal(expires) {
			t.Fatalf("issue = %+v", p)
		}
		return nil
	})
	got, err := uc.RegenerateLiveScreenLink(context.Background(), eventID, uuid.Nil, uuid.Nil)
	if err != nil || got.Token == "" || got.ID == oldID || got.ExpiresAt == nil || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("regenerated = %+v, %v", got, err)
	}
}

func TestRegenerateWithoutALinkIsNotFound(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID := uuid.Must(uuid.NewV7())
	expectFirstScreen(t, q, eventID)
	q.EXPECT().GetActiveEventLiveScreenLink(gomock.Any(), gomock.Any()).Return(postgres.EventLiveScreenLink{}, pgx.ErrNoRows)
	if _, err := uc.RegenerateLiveScreenLink(context.Background(), eventID, uuid.Nil, uuid.Nil); !errors.Is(err, eventContentModel.ErrLiveScreenLinkNotFound.Err()) {
		t.Fatalf("regenerate without a link = %v", err)
	}
}

func TestGetAndRevokeLiveScreenLink(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID, screenID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventLiveScreen(gomock.Any(), postgres.GetEventLiveScreenParams{ID: screenID, EventID: eventID}).Return(liveScreenRow(t, eventID, screenID, "Фойє"), nil).Times(2)
	q.EXPECT().GetActiveEventLiveScreenLink(gomock.Any(), gomock.Any()).Return(postgres.EventLiveScreenLink{}, pgx.ErrNoRows)
	if link, err := uc.GetLiveScreenLink(context.Background(), eventID, screenID); err != nil || link != nil {
		t.Fatalf("no link = %+v, %v", link, err)
	}
	q.EXPECT().RevokeEventLiveScreenLinks(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.RevokeEventLiveScreenLinksParams) (int64, error) {
		if p.EventID != eventID || p.ScreenID != screenID {
			t.Fatalf("revoke %+v", p)
		}
		return 0, nil
	})
	if err := uc.RevokeLiveScreenLink(context.Background(), eventID, screenID); err != nil {
		t.Fatalf("turning off without a link = %v", err)
	}
}

// A screen of another event (or a deleted one) is not found; its link is
// never read or written.
func TestLiveScreenLinkOfAnUnknownScreen(t *testing.T) {
	uc, q := liveScreenUseCase(t)
	eventID, screenID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventLiveScreen(gomock.Any(), gomock.Any()).Return(postgres.EventLiveScreen{}, pgx.ErrNoRows).Times(3)
	if _, err := uc.GetLiveScreenLink(context.Background(), eventID, screenID); !errors.Is(err, eventContentModel.ErrLiveScreenNotFound.Err()) {
		t.Fatalf("get = %v", err)
	}
	if err := uc.RevokeLiveScreenLink(context.Background(), eventID, screenID); !errors.Is(err, eventContentModel.ErrLiveScreenNotFound.Err()) {
		t.Fatalf("revoke = %v", err)
	}
	if _, err := uc.RegenerateLiveScreenLink(context.Background(), eventID, screenID, uuid.Nil); !errors.Is(err, eventContentModel.ErrLiveScreenNotFound.Err()) {
		t.Fatalf("regenerate = %v", err)
	}
}

func TestResolveLiveScreenToken(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	link, token, _ := eventContentModel.NewLiveScreenLink(eventID, uuid.Must(uuid.NewV7()), uuid.Nil, now, eventContentModel.LiveScreenExpiryDay, nil)
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
