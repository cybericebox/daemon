package event_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// eventDataURI is the data: URI the preview inlines an image as.
func eventDataURI(contentType string, data []byte) string {
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func TestPreviewEventEmail_RendersEventScopedType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil)
	q.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)

	out, err := uc.PreviewEventEmail(context.Background(), eventID, emailUseCase.PreviewInput{
		NotificationType: "participant.approval_registration.approved",
		Subject:          "Hi {{.user_first_name}}",
		Preheader:        "pre",
		Body:             json.RawMessage(`[{"type":"logo"}]`),
		Values:           map[string]string{"user_first_name": "Ada"},
	})
	require.NoError(t, err)
	require.Equal(t, "Hi Ada", out.Subject)
	require.Equal(t, "pre", out.Preheader)
	require.Contains(t, out.HTML, `src="`+eventDataURI(branding.LogoContentType, branding.LogoPNG())+`"`)
	require.NotContains(t, out.HTML, "cid:")
	require.NotContains(t, out.HTML, "/api/")
}

func TestPreviewEventEmail_RejectsPlatformOnlyType(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)

	_, err := uc.PreviewEventEmail(context.Background(), eventID, emailUseCase.PreviewInput{
		NotificationType: "event.manager.assigned",
		Body:             json.RawMessage(`[]`),
	})
	require.True(t, notificationModel.ErrTemplateTypeNotEventScoped.Err().Is(err), "got %v", err)
}

func TestStreamEventEmailImage_RequiresReferenceUsableByThisEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	usable := m.addFile(3)
	// e.g. referenced only by ANOTHER Event's template, or not a template image at all
	foreign := m.addFile(3)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, now), nil).Times(2)
	expectFileUsable(q, usable, eventID, true)
	expectFileUsable(q, foreign, eventID, false)

	rc, f, err := uc.StreamEventEmailImage(context.Background(), eventID, usable)
	require.NoError(t, err)
	require.Equal(t, usable, f.ID)
	b, _ := io.ReadAll(rc)
	require.Equal(t, "img", strings.TrimSpace(string(b)))
	require.NoError(t, rc.Close())

	_, _, err = uc.StreamEventEmailImage(context.Background(), eventID, foreign)
	require.True(t, mediaModel.ErrFileNotFound.Err().Is(err), "got %v", err)
}

func TestPreviewEventEmail_UnrenderableDraftIsInvalidInput(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)

	_, err := uc.PreviewEventEmail(context.Background(), eventID, emailUseCase.PreviewInput{
		NotificationType: "participant.approval_registration.approved",
		Subject:          "Hi {{.user_first_name",
		Body:             json.RawMessage(`[]`),
	})
	require.True(t, notificationModel.ErrTemplatePreviewInvalid.Err().Is(err), "got %v", err)
}

func TestEventEmailBrandLogo_RequiresEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newTemplateUC(q, newTemplateMediaFake())
	known, unknown := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), known).Return(rollingEventRow(known, time.Now().UTC()), nil)
	q.EXPECT().GetEventByID(gomock.Any(), unknown).Return(postgres.Event{}, pgx.ErrNoRows)

	data, contentType, err := uc.EventEmailBrandLogo(context.Background(), known)
	require.NoError(t, err)
	require.Equal(t, branding.LogoContentType, contentType)
	require.Equal(t, branding.LogoPNG(), data)

	_, _, err = uc.EventEmailBrandLogo(context.Background(), unknown)
	require.True(t, eventModel.ErrEventNotFound.Err().Is(err), "got %v", err)
}

// Every uploaded image the draft renders — directly or through a preset, as
// dispatch resolves it — is inlined only when usable by this Event.
func TestPreviewEventEmail_InlinesImagesUsableByEvent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	own, viaPreset := m.addFile(3), m.addFile(3)
	presetID := uuid.Must(uuid.NewV7())
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().ListEmailBlockPresets(gomock.Any()).Return([]postgres.NotificationEmailBlockPreset{{
		ID: presetID, Blocks: []byte(`[{"type":"image","file_id":"` + viaPreset.String() + `"}]`),
	}}, nil)
	expectFileUsable(q, own, eventID, true)
	expectFileUsable(q, viaPreset, eventID, true)

	out, err := uc.PreviewEventEmail(context.Background(), eventID, emailUseCase.PreviewInput{
		NotificationType: "participant.approval_registration.approved",
		Subject:          "Hi",
		Body: json.RawMessage(`[{"type":"image","file_id":"` + own.String() + `"},` +
			`{"type":"preset","preset_id":"` + presetID.String() + `"}]`),
	})
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(out.HTML, `src="`+eventDataURI("image/png", []byte("img"))+`"`), out.HTML)
	require.NotContains(t, out.HTML, "/api/")
}

func TestPreviewEventEmail_RejectsForeignImage(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	m := newTemplateMediaFake()
	// an existing PNG no platform template, preset or template of THIS Event uses
	foreign := m.addFile(3)
	uc := newTemplateUC(q, m)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(rollingEventRow(eventID, time.Now().UTC()), nil)
	q.EXPECT().ListEmailBlockPresets(gomock.Any()).Return(nil, nil)
	expectFileUsable(q, foreign, eventID, false)

	_, err := uc.PreviewEventEmail(context.Background(), eventID, emailUseCase.PreviewInput{
		NotificationType: "participant.approval_registration.approved",
		Subject:          "Hi",
		Body:             json.RawMessage(`[{"type":"image","file_id":"` + foreign.String() + `"}]`),
	})
	require.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err), "got %v", err)
}
