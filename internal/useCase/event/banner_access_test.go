package event_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	event "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

type bannerAccessMedia struct {
	brandMediaFake
	streams int
}

func (m *bannerAccessMedia) StreamFile(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	m.streams++
	return m.brandMediaFake.StreamFile(ctx, id)
}

func TestBannerStreamsRecheckPublicationStaffAccessAndReferences(t *testing.T) {
	for _, kind := range []string{"preview", "content"} {
		t.Run(kind, func(t *testing.T) {
			eventID, fileID, staff := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q := newFormGateMock(gomock.NewController(t))
			published, member := true, true
			q.EXPECT().GetEventByID(gomock.Any(), eventID).DoAndReturn(func(context.Context, uuid.UUID) (postgres.Event, error) {
				if published {
					return startedEvent(eventID, time.Now()), nil
				}
				return postgres.Event{ID: eventID}, nil
			}).AnyTimes()
			q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: staff}).DoAndReturn(func(context.Context, postgres.GetEventManagerParams) (postgres.EventManager, error) {
				if member {
					return postgres.EventManager{EventID: eventID, UserID: staff, Role: int16(eventManagerModel.RoleViewer)}, nil
				}
				return postgres.EventManager{}, pgx.ErrNoRows
			}).AnyTimes()
			media := &bannerAccessMedia{brandMediaFake: brandMediaFake{
				file:        mediaModel.File{ID: fileID, ContentType: "image/png"},
				previewRefs: []uuid.UUID{fileID}, contentRefs: map[uuid.UUID][]uuid.UUID{eventID: {fileID}},
			}}
			u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
			stream := streamMedia(u, event.EventMediaPreviewPicture)
			if kind == "content" {
				stream = streamMedia(u, event.EventMediaContentImage)
			}
			read := func(ctx context.Context) error {
				r, _, err := stream(ctx, eventID, fileID)
				if r != nil {
					require.NoError(t, r.Close())
				}
				return err
			}
			require.NoError(t, read(context.Background()))
			published = false
			require.ErrorIs(t, read(context.Background()), eventModel.ErrEventNotFound.Err())
			require.Equal(t, 1, media.streams, "unpublishing must deny before opening cached media")
			staffCtx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: staff, Role: rbac.RoleUser})
			require.NoError(t, read(staffCtx))
			member = false
			require.ErrorIs(t, read(staffCtx), eventModel.ErrEventNotFound.Err())
			require.Equal(t, 2, media.streams, "revoking the viewer must take effect on the next request")
			published = true
			media.previewRefs = []uuid.UUID{uuid.Must(uuid.NewV7())}
			media.contentRefs[eventID] = nil
			require.ErrorIs(t, read(context.Background()), mediaModel.ErrFileNotFound.Err())
			require.Equal(t, 2, media.streams, "removing or replacing the reference must reject an old URL")
		})
	}
}
