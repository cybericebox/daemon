package event_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestFaviconPNGIsPublishedOnlyAfterAppearanceSave(t *testing.T) {
	ctx := context.Background()
	eventID, actor, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, BrandColor: "#211A52"}, nil).AnyTimes()
	media := &brandMediaFake{file: mediaModel.File{ID: fileID}}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
	png := realPNG("favicon")
	id, err := u.UploadEventBrandDraft(ctx, eventID, actor, "favicon", bytes.NewReader(png), int64(len(png)))
	require.NoError(t, err)
	require.Equal(t, fileID, id)
	require.Equal(t, "image/png", media.file.ContentType)
	require.Empty(t, media.faviconRefs, "upload is a draft until the appearance form is saved")

	input := event.EventAppearanceInput{Brand: "#211A52", Logo: event.BrandAssetChange{Action: "keep"}, Favicon: event.BrandAssetChange{Action: "replace", FileID: fileID}}
	saved, err := u.SaveEventAppearance(ctx, eventID, actor, input)
	require.NoError(t, err)
	wantURL := "/api/events/" + eventID.String() + "/favicon/" + fileID.String()
	require.Equal(t, wantURL, saved.FaviconURL)
	require.Equal(t, []uuid.UUID{fileID}, media.faviconRefs)
	url, err := u.EventFaviconURL(ctx, eventID)
	require.NoError(t, err)
	require.Equal(t, wantURL, url, "a subsequent event-info read must keep the saved reference")
	reader, mime, err := streamMedia(u, event.EventMediaFavicon)(ctx, eventID, fileID)
	require.NoError(t, err)
	require.Equal(t, "image/png", mime)
	require.NoError(t, reader.Close())

	input.Favicon = event.BrandAssetChange{Action: "remove"}
	saved, err = u.SaveEventAppearance(ctx, eventID, actor, input)
	require.NoError(t, err)
	require.Empty(t, saved.FaviconURL)
	require.Empty(t, media.faviconRefs)
	_, _, err = streamMedia(u, event.EventMediaFavicon)(ctx, eventID, fileID)
	require.ErrorIs(t, err, mediaModel.ErrFileNotFound.Err(), "removed icons are no longer served")
}

func TestFaviconUploadKeepsPNGAndByteValidation(t *testing.T) {
	ctx := context.Background()
	eventID, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name string
		data []byte
		size int64
		err  error
	}{
		{"non-PNG", []byte("GIF89a"), 6, eventModel.ErrEventBrandDraftInvalid.Err()},
		{"declared oversize", realPNG("icon"), (512 << 10) + 1, eventModel.ErrEventBrandDraftInvalid.Err()},
		{"actual oversize", make([]byte, (512<<10)+1), 1, eventModel.ErrEventBrandDraftInvalid.Err()},
		{"unreadable PNG", []byte("\x89PNG\r\n\x1a\n"), 8, mediaModel.ErrImageInvalid.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newFormGateMock(gomock.NewController(t))
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil)
			media := &brandMediaFake{}
			u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
			_, err := u.UploadEventBrandDraft(ctx, eventID, actor, "favicon", bytes.NewReader(tc.data), tc.size)
			require.ErrorIs(t, err, tc.err)
			require.Empty(t, media.file.Name, "invalid input must not reach file storage")
		})
	}
}
