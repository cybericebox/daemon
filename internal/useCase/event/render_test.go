package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"testing"
	"time"
)

// No expectations for counts, infrastructure or participant queries: the
// manifest must stay a document-free and statistics-free access/revision read.
func TestRenderManifestPublicPrivateAndBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		hidden, preview, allowed bool
		wantErr                  bool
	}{
		{"public", false, false, true, false}, {"staff grant after publication", false, true, true, false}, {"hidden", true, false, true, true}, {"staff published preview", true, true, true, false}, {"private custom page", false, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
			id := uuid.Must(uuid.NewV4())
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			publish := now.Add(-time.Hour)
			if tc.hidden {
				publish = now.Add(-time.Hour)
			}
			q.EXPECT().GetEventByID(gomock.Any(), id).Return(postgres.Event{ID: id, Name: "Event", Tag: "summer", LifecycleConfigured: !tc.hidden, PublishAt: publish, StartAt: now.Add(-30 * time.Minute), FinishAt: pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}, WithdrawAt: pgtype.Timestamptz{Time: now.Add(500 * time.Millisecond), Valid: true}}, nil)
			if !tc.hidden || tc.preview {
				q.EXPECT().GetEventRenderMetadata(gomock.Any(), postgres.GetEventRenderMetadataParams{EventID: id, PageSlug: "rules"}).Return(postgres.GetEventRenderMetadataRow{Revision: "revision-one", Slug: "rules", Title: "Rules", Allowed: tc.allowed}, nil)
				if tc.allowed {
					q.EXPECT().GetEventConfig(gomock.Any(), id).Return(postgres.EventConfig{EventID: id}, nil)
				}
			}
			uc := NewEventUseCase(Dependencies{Repo: q})
			v, err := uc.GetEventRenderManifest(context.Background(), id, "rules", tc.preview, now)
			if tc.wantErr {
				require.ErrorIs(t, err, eventModel.ErrEventNotFound.Err())
				return
			}
			require.NoError(t, err)
			require.Equal(t, renderRevision("revision-one", v.Event.Status), v.Revision)
			require.Equal(t, now.Add(500*time.Millisecond).UnixMilli(), v.CacheUntil)
			if tc.preview {
				require.Equal(t, "staff", v.Audience)
			} else {
				require.Equal(t, "public", v.Audience)
			}
		})
	}
}

func TestRenderPageRejectsConcurrentPublicationBeforeStatistics(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	id := uuid.Must(uuid.NewV4())
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(postgres.Event{ID: id, LifecycleConfigured: true, PublishAt: now.Add(-time.Hour), StartAt: now.Add(time.Hour)}, nil)
	q.EXPECT().GetEventRenderMetadata(gomock.Any(), postgres.GetEventRenderMetadataParams{EventID: id}).Return(postgres.GetEventRenderMetadataRow{Revision: "r1", Allowed: true}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), id).Return(postgres.EventConfig{EventID: id}, nil)
	q.EXPECT().GetEventRenderDocument(gomock.Any(), postgres.GetEventRenderDocumentParams{EventID: id}).Return(postgres.GetEventRenderDocumentRow{Revision: "r2", Allowed: true, Document: []byte(`{"blocks":[]}`)}, nil)
	uc := NewEventUseCase(Dependencies{Repo: q})
	_, err := uc.GetEventRenderPage(context.Background(), id, "", renderRevision("r1", eventModel.LifecyclePublished), false, now)
	require.ErrorIs(t, err, ErrRenderRevisionChanged)
}
