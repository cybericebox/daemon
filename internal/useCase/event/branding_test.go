package event_test

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type brandMediaFake struct {
	file        mediaModel.File
	refs        []uuid.UUID
	previewRefs []uuid.UUID
	faviconRefs []uuid.UUID
	contentRefs map[uuid.UUID][]uuid.UUID
	mime        string
	blob        []byte
}

func (m *brandMediaFake) UploadFile(_ context.Context, name, contentType string, _ io.Reader, by uuid.UUID) (mediaModel.File, error) {
	m.mime = contentType
	m.file.Name = name
	m.file.ContentType = contentType
	m.file.CreatedBy = uuid.NullUUID{UUID: by, Valid: true}
	m.file.CreatedAt = time.Now()
	return m.file, nil
}
func (m *brandMediaFake) StreamFile(_ context.Context, _ uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	if m.blob != nil {
		return io.NopCloser(bytes.NewReader(m.blob)), m.file, nil
	}
	return io.NopCloser(strings.NewReader("image")), m.file, nil
}

func TestResolveEventEmailBrand_UsesEventThemeAndLogo(t *testing.T) {
	ctx := context.Background()
	eventID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, BrandColor: "#123456", AccentColor: "#E0FFFF", ThemeVersion: 2,
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Name: "Spring CTF"}, nil)
	logo := image.NewNRGBA(image.Rect(0, 0, 512, 128))
	logo.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var raw bytes.Buffer
	require.NoError(t, png.Encode(&raw, logo))
	media := &brandMediaFake{
		file: mediaModel.File{ID: fileID, ContentType: "image/png"},
		refs: []uuid.UUID{fileID}, blob: raw.Bytes(),
	}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
	brand, err := u.ResolveEventEmailBrand(ctx, eventID)
	require.NoError(t, err)
	require.Equal(t, "#123456", brand.Colors.Brand)
	require.Equal(t, "#E0FFFF", brand.Colors.Accent)
	require.Equal(t, "#000000", brand.Colors.OnAccent)
	require.Equal(t, "image/png", brand.LogoContentType)
	require.Equal(t, "Spring CTF", brand.LogoAlt)
	decoded, err := png.Decode(bytes.NewReader(brand.Logo))
	require.NoError(t, err)
	require.Equal(t, image.Rect(0, 0, 256, 64), decoded.Bounds())
}
func (m *brandMediaFake) GetFile(_ context.Context, _ uuid.UUID) (mediaModel.File, error) {
	return m.file, nil
}
func (m *brandMediaFake) GetReferences(_ context.Context, refType string, owner uuid.UUID) ([]uuid.UUID, error) {
	switch refType {
	case mediaModel.RefTypeEventLogo:
		return m.refs, nil
	case mediaModel.RefTypeEventPreviewPicture:
		return m.previewRefs, nil
	case mediaModel.RefTypeEventFavicon:
		return m.faviconRefs, nil
	case mediaModel.RefTypeEventContentImage:
		return m.contentRefs[owner], nil
	default:
		panic("wrong reference type")
	}
}
func (m *brandMediaFake) ReplaceReferences(_ context.Context, refType string, owner uuid.UUID, ids []uuid.UUID) error {
	switch refType {
	case mediaModel.RefTypeEventLogo:
		m.refs = ids
	case mediaModel.RefTypeEventPreviewPicture:
		m.previewRefs = ids
	case mediaModel.RefTypeEventFavicon:
		m.faviconRefs = ids
	case mediaModel.RefTypeEventContentImage:
		if m.contentRefs == nil {
			m.contentRefs = make(map[uuid.UUID][]uuid.UUID)
		}
		m.contentRefs[owner] = ids
	default:
		panic("wrong reference type")
	}
	return nil
}

func (m *brandMediaFake) AddReference(_ context.Context, refType string, owner, fileID uuid.UUID) error {
	if refType != mediaModel.RefTypeEventContentImage {
		panic("wrong reference type")
	}
	if m.contentRefs == nil {
		m.contentRefs = make(map[uuid.UUID][]uuid.UUID)
	}
	m.contentRefs[owner] = append(m.contentRefs[owner], fileID)
	return nil
}

func TestEventContentImageIsIndependentAndEventScoped(t *testing.T) {
	ctx := context.Background()
	eventID, otherEventID, actor, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, id uuid.UUID) (postgres.Event, error) {
		return startedEvent(id, time.Now()), nil
	}).AnyTimes()
	media := &brandMediaFake{file: mediaModel.File{ID: fileID}, previewRefs: []uuid.UUID{uuid.Must(uuid.NewV7())}}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
	png := realPNG("content")
	url, err := u.UploadEventContentImage(ctx, eventID, actor, bytes.NewReader(png), int64(len(png)))
	require.NoError(t, err)
	require.Equal(t, "/api/events/"+eventID.String()+"/content-images/"+fileID.String(), url)
	require.Equal(t, []uuid.UUID{fileID}, media.contentRefs[eventID])
	require.Len(t, media.previewRefs, 1, "upload must leave the event preview untouched")
	reader, contentType, err := u.StreamEventContentImage(ctx, eventID, fileID)
	require.NoError(t, err)
	require.Equal(t, "image/png", contentType)
	require.NoError(t, reader.Close())
	_, _, err = u.StreamEventContentImage(ctx, otherEventID, fileID)
	require.Error(t, err, "another event must not serve this image")
}

func TestEventContentImageAcceptsAnimatedGIF(t *testing.T) {
	ctx := context.Background()
	eventID, actor, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil).AnyTimes()
	media := &brandMediaFake{file: mediaModel.File{ID: fileID}}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
	first := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	second := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	second.Pix[0] = 1
	var animated bytes.Buffer
	require.NoError(t, gif.EncodeAll(&animated, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{5, 5}}))
	url, err := u.UploadEventContentImage(ctx, eventID, actor, bytes.NewReader(animated.Bytes()), int64(animated.Len()))
	require.NoError(t, err)
	require.Equal(t, "/api/events/"+eventID.String()+"/content-images/"+fileID.String(), url)
	require.Equal(t, "image/gif", media.mime)
	reader, contentType, err := u.StreamEventContentImage(ctx, eventID, fileID)
	require.NoError(t, err)
	require.Equal(t, "image/gif", contentType)
	require.NoError(t, reader.Close())
}

func TestEventLogoUploadReadAndReset(t *testing.T) {
	ctx := context.Background()
	eventID, actor, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil).AnyTimes()
	media := &brandMediaFake{file: mediaModel.File{ID: fileID, ContentType: "image/png"}}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
	if got, err := u.EventLogoURL(ctx, eventID); err != nil || got != "" {
		t.Fatalf("empty logo: %q, %v", got, err)
	}
	if _, err := u.UploadEventLogo(ctx, eventID, actor, strings.NewReader("not an image"), 12); err == nil {
		t.Fatal("accepted non-image")
	}
	if _, err := u.UploadEventLogo(ctx, eventID, actor, bytes.NewReader(make([]byte, (2<<20)+1)), (2<<20)+1); err == nil {
		t.Fatal("accepted oversized logo")
	}
	png := realPNG("minimal")
	url, err := u.UploadEventLogo(ctx, eventID, actor, bytes.NewReader(png), int64(len(png)))
	want := "/api/events/" + eventID.String() + "/logo/" + fileID.String()
	if err != nil || url != want || media.mime != "image/png" {
		t.Fatalf("upload: %q, %q, %v", url, media.mime, err)
	}
	if got, err := u.EventLogoURL(ctx, eventID); err != nil || got != want {
		t.Fatalf("logo url: %q, %v", got, err)
	}
	if _, _, err := u.StreamEventLogo(ctx, eventID, uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("served a non-current logo")
	}
	reader, mime, err := u.StreamEventLogo(ctx, eventID, fileID)
	if err != nil || mime != "image/png" {
		t.Fatalf("stream: %q, %v", mime, err)
	}
	_ = reader.Close()
	if err := u.RemoveEventLogo(ctx, eventID); err != nil {
		t.Fatal(err)
	}
	if got, err := u.EventLogoURL(ctx, eventID); err != nil || got != "" {
		t.Fatalf("reset: %q, %v", got, err)
	}
}

func TestEventBrandDraftDoesNotPublishAndRejectsOtherActor(t *testing.T) {
	ctx := context.Background()
	eventID, actor, otherActor, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil).AnyTimes()
	media := &brandMediaFake{file: mediaModel.File{ID: fileID}}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
	png := realPNG("minimal")
	gotID, err := u.UploadEventBrandDraft(ctx, eventID, actor, "logo", bytes.NewReader(png), int64(len(png)))
	if err != nil || gotID != fileID || media.file.Name != "event-brand-draft:"+eventID.String()+":logo" {
		t.Fatalf("draft: id=%s name=%q err=%v", gotID, media.file.Name, err)
	}
	if len(media.refs) != 0 {
		t.Fatal("draft was published before Save")
	}
	_, err = u.SaveEventAppearance(ctx, eventID, otherActor, event.EventAppearanceInput{Logo: event.BrandAssetChange{Action: "replace", FileID: fileID}, Favicon: event.BrandAssetChange{Action: "keep"}})
	if err == nil || len(media.refs) != 0 {
		t.Fatalf("other actor claimed draft: refs=%v err=%v", media.refs, err)
	}
}

func TestEventPreviewPictureUploadReadAndReset(t *testing.T) {
	ctx := context.Background()
	eventID, actor, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil).AnyTimes()
	media := &brandMediaFake{file: mediaModel.File{ID: fileID, ContentType: "image/png"}}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media, PublicAPIBaseURL: "https://api.example.test"})
	if _, err := u.UploadEventPreviewPicture(ctx, eventID, actor, strings.NewReader("not an image"), 12); err == nil {
		t.Fatal("accepted non-image")
	}
	if _, err := u.UploadEventPreviewPicture(ctx, eventID, actor, bytes.NewReader(make([]byte, (5<<20)+1)), (5<<20)+1); err == nil {
		t.Fatal("accepted oversized preview")
	}
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	row := postgres.EventConfig{EventID: eventID, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true}}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)
	wantURL := "https://api.example.test/api/events/" + eventID.String() + "/preview-picture/" + fileID.String()
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventConfigParams) (int64, error) {
		if arg.PreviewPicture != wantURL || !arg.ExpectedUpdatedAt.Time.Equal(t0) {
			t.Fatalf("unexpected preview update: %+v", arg)
		}
		return 1, nil
	})
	png := realPNG("minimal")
	gotURL, err := u.UploadEventPreviewPicture(ctx, eventID, actor, bytes.NewReader(png), int64(len(png)))
	if err != nil || gotURL != wantURL || len(media.previewRefs) != 1 || media.previewRefs[0] != fileID {
		t.Fatalf("upload: %q, refs %+v, %v", gotURL, media.previewRefs, err)
	}
	if _, _, err := u.StreamEventPreviewPicture(ctx, eventID, uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("served a non-current preview")
	}
	reader, mime, err := u.StreamEventPreviewPicture(ctx, eventID, fileID)
	if err != nil || mime != "image/png" {
		t.Fatalf("stream: %q, %v", mime, err)
	}
	_ = reader.Close()
	row.PreviewPicture = wantURL
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil).Times(2)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventConfigParams) (int64, error) {
		if arg.PreviewPicture != "" {
			t.Fatalf("reset did not clear preview: %+v", arg)
		}
		return 1, nil
	})
	if err := u.RemoveEventPreviewPicture(ctx, eventID, actor); err != nil || len(media.previewRefs) != 0 {
		t.Fatalf("reset: refs %+v, %v", media.previewRefs, err)
	}
}

func TestEventPreviewPictureUploadRestoresReferenceOnConfigConflict(t *testing.T) {
	ctx := context.Background()
	eventID, actor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	oldFileID, newFileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID}, nil)
	t0 := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	row := postgres.EventConfig{EventID: eventID, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true}}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil).Times(2)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	media := &brandMediaFake{file: mediaModel.File{ID: newFileID, ContentType: "image/png"}, previewRefs: []uuid.UUID{oldFileID}}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media, PublicAPIBaseURL: "https://api.example.test"})
	png := realPNG("minimal")
	if _, err := u.UploadEventPreviewPicture(ctx, eventID, actor, bytes.NewReader(png), int64(len(png))); err == nil {
		t.Fatal("expected config conflict")
	}
	if len(media.previewRefs) != 1 || media.previewRefs[0] != oldFileID {
		t.Fatalf("previous reference was not restored: %+v", media.previewRefs)
	}
}

// L21: a file the actor named like a brand draft through another upload route (any content type) must not become
// a public logo: the draft is checked for what it is, not only for who made it and what it is called.
func TestEventBrandDraftOfANonImageIsRefused(t *testing.T) {
	ctx := context.Background()
	eventID, actor, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q := newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil).AnyTimes()
	media := &brandMediaFake{file: mediaModel.File{ID: fileID}}
	u := event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media})
	// Stored by another route: right name and owner, but an HTML document.
	media.file.Name = "event-brand-draft:" + eventID.String() + ":logo"
	media.file.ContentType = "text/html"
	media.file.CreatedBy = uuid.NullUUID{UUID: actor, Valid: true}
	media.file.CreatedAt = time.Now()
	_, err := u.SaveEventAppearance(ctx, eventID, actor, event.EventAppearanceInput{Logo: event.BrandAssetChange{Action: "replace", FileID: fileID}, Favicon: event.BrandAssetChange{Action: "keep"}})
	if !errors.Is(err, eventModel.ErrEventBrandDraftInvalid.Err()) || len(media.refs) != 0 {
		t.Fatalf("a non-image draft must be refused as an invalid draft: refs=%v err=%v", media.refs, err)
	}
}

// Before the event is published its media is not public: only staff of the event see it.
func TestEventMediaOfAnUnpublishedEventIsForItsStaffOnly(t *testing.T) {
	eventID, logoID, staff, stranger := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	build := func(t *testing.T, e postgres.Event) (*event.EventUseCase, *brandMediaFake, *postgresMocks.MockQuerier) {
		q := newFormGateMock(gomock.NewController(t))
		q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(e, nil).AnyTimes()
		media := &brandMediaFake{file: mediaModel.File{ID: logoID}, refs: []uuid.UUID{logoID}, previewRefs: []uuid.UUID{logoID}, faviconRefs: []uuid.UUID{logoID},
			contentRefs: map[uuid.UUID][]uuid.UUID{eventID: {logoID}}}
		return event.NewEventUseCase(event.Dependencies{Repo: q, BrandMedia: media}), media, q
	}
	unpublished := postgres.Event{ID: eventID, LifecycleConfigured: false}
	published := startedEvent(eventID, time.Now())
	streams := map[string]func(*event.EventUseCase, context.Context) error{
		"logo": func(u *event.EventUseCase, c context.Context) error {
			_, _, err := u.StreamEventLogo(c, eventID, logoID)
			return err
		},
		"favicon": func(u *event.EventUseCase, c context.Context) error {
			_, _, err := u.StreamEventFavicon(c, eventID, logoID)
			return err
		},
		"preview": func(u *event.EventUseCase, c context.Context) error {
			_, _, err := u.StreamEventPreviewPicture(c, eventID, logoID)
			return err
		},
		"content": func(u *event.EventUseCase, c context.Context) error {
			_, _, err := u.StreamEventContentImage(c, eventID, logoID)
			return err
		},
	}
	for name, stream := range streams {
		t.Run(name, func(t *testing.T) {
			u, _, q := build(t, published)
			_ = q
			if err := stream(u, context.Background()); err != nil {
				t.Fatalf("a published event's media is public: %v", err)
			}
			// unpublished: not found for an anonymous caller and for a stranger
			u, _, _ = build(t, unpublished)
			if err := stream(u, context.Background()); !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
				t.Fatalf("anonymous: want not found, got %v", err)
			}
			strangerCtx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: stranger, Role: rbac.RoleUser})
			q2 := newFormGateMock(gomock.NewController(t))
			q2.EXPECT().GetEventByID(gomock.Any(), eventID).Return(unpublished, nil).AnyTimes()
			q2.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows).AnyTimes()
			media := &brandMediaFake{file: mediaModel.File{ID: logoID}, refs: []uuid.UUID{logoID}, previewRefs: []uuid.UUID{logoID}, faviconRefs: []uuid.UUID{logoID},
				contentRefs: map[uuid.UUID][]uuid.UUID{eventID: {logoID}}}
			u2 := event.NewEventUseCase(event.Dependencies{Repo: q2, BrandMedia: media})
			if err := stream(u2, strangerCtx); !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
				t.Fatalf("a signed-in stranger: want not found, got %v", err)
			}
			// a platform admin with events.read reads it (implicit viewer)
			adminCtx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: staff, Role: rbac.RoleAdmin})
			if err := stream(u2, adminCtx); err != nil {
				t.Fatalf("platform staff set the event up: %v", err)
			}
		})
	}
}
