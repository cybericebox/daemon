package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	event "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"testing"
	"time"
)

func TestAppearancePersistsWebsiteSignatureAndPreservesOtherSettings(t *testing.T) {
	eventID, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	signature := json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"© Event organizer"}]}]}}`)
	for _, mode := range []string{"replace", "omit", "clear", "unsafe"} {
		t.Run(mode, func(t *testing.T) {
			q := newFormGateMock(gomock.NewController(t))
			row := postgres.EventConfig{EventID: eventID, BrandColor: "#211A52", ThemeVersion: 1, Registration: 2, MaxTeamSize: 7, FooterSignature: signature, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}
			input := event.EventAppearanceInput{Brand: "#211A52", Logo: event.BrandAssetChange{Action: "keep"}, Favicon: event.BrandAssetChange{Action: "keep"}}
			switch mode {
			case "replace":
				input.FooterSignature = json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Partner"}]}]}}`)
			case "clear":
				input.FooterSignature = json.RawMessage("null")
			case "unsafe":
				input.FooterSignature = json.RawMessage(`{"root":{"type":"root","children":[{"type":"link","url":"javascript:alert(1)","children":[{"type":"text","text":"bad"}]}]}}`)
			}
			reads := 2
			if mode == "omit" {
				reads = 1
			}
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil).Times(reads)
			if mode == "replace" || mode == "clear" {
				q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, written postgres.UpdateEventConfigParams) (int64, error) {
					require.Equal(t, eventID, written.EventID)
					require.Equal(t, int16(2), written.Registration)
					require.Equal(t, int32(7), written.MaxTeamSize)
					if mode == "clear" {
						require.Empty(t, written.FooterSignature)
					} else {
						require.JSONEq(t, string(input.FooterSignature), string(written.FooterSignature))
					}
					require.Equal(t, actor, written.UpdatedBy.UUID)
					return 1, nil
				})
			}
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: &brandMediaFake{}})
			out, err := uc.SaveEventAppearance(context.Background(), eventID, actor, input)
			if mode == "unsafe" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			switch mode {
			case "omit":
				require.JSONEq(t, string(signature), string(out.Config.FooterSignature))
			case "clear":
				require.Nil(t, out.Config.FooterSignature)
			default:
				require.JSONEq(t, string(input.FooterSignature), string(out.Config.FooterSignature))
			}
		})
	}
}

type failingFooterMedia struct{ brandMediaFake }

func (*failingFooterMedia) ReplaceReferences(context.Context, string, uuid.UUID, []uuid.UUID) error {
	return errors.New("media unavailable")
}

func TestAppearanceRollbackDoesNotOverwriteANewerSignature(t *testing.T) {
	eventID, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	q := newFormGateMock(gomock.NewController(t))
	row := postgres.EventConfig{EventID: eventID, BrandColor: "#211A52", ThemeVersion: 1, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil).Times(2)
	var savedAt time.Time
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, in postgres.UpdateEventConfigParams) (int64, error) {
		savedAt = in.UpdatedAt.Time
		return 1, nil
	})
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).DoAndReturn(func(context.Context, uuid.UUID) (postgres.EventConfig, error) {
		row.UpdatedAt.Time = savedAt.Add(time.Second)
		return row, nil
	})
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: &failingFooterMedia{}})
	_, err := uc.SaveEventAppearance(context.Background(), eventID, actor, event.EventAppearanceInput{Brand: "#211A52", FooterSignature: json.RawMessage("null"), Logo: event.BrandAssetChange{Action: "remove"}, Favicon: event.BrandAssetChange{Action: "keep"}})
	require.ErrorContains(t, err, "media unavailable")
	require.ErrorContains(t, err, "appearance changed before rollback")
	// A second UpdateEventConfig would fail the mock: the newer appearance stays intact.
}

func TestAppearanceRollbackAcceptsPostgresTimestampPrecision(t *testing.T) {
	eventID, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	signature := json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Original partner"}]}]}}`)
	q := newFormGateMock(gomock.NewController(t))
	row := postgres.EventConfig{EventID: eventID, BrandColor: "#211A52", ThemeVersion: 1, FooterSignature: signature, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil).Times(2)
	var savedAt time.Time
	writes := 0
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, in postgres.UpdateEventConfigParams) (int64, error) {
		writes++
		if writes == 1 {
			savedAt = in.UpdatedAt.Time
		} else {
			require.JSONEq(t, string(signature), string(in.FooterSignature))
			require.Equal(t, savedAt.Truncate(time.Microsecond), in.ExpectedUpdatedAt.Time)
		}
		return 1, nil
	}).Times(2)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).DoAndReturn(func(context.Context, uuid.UUID) (postgres.EventConfig, error) {
		row.UpdatedAt.Time = savedAt.Truncate(time.Microsecond)
		row.FooterSignature = nil
		return row, nil
	})
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: &failingFooterMedia{}})
	_, err := uc.SaveEventAppearance(context.Background(), eventID, actor, event.EventAppearanceInput{Brand: "#211A52", FooterSignature: json.RawMessage("null"), Logo: event.BrandAssetChange{Action: "remove"}, Favicon: event.BrandAssetChange{Action: "keep"}})
	require.ErrorContains(t, err, "media unavailable")
	require.NotContains(t, err.Error(), "appearance changed before rollback")
	require.Equal(t, 2, writes, "a failed media save must restore the signature when only DB timestamp precision differs")
}
