package event_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// previewContractUC is a read-only Event member: manage is forbidden, so the
// preview and image routes must be gated by requireRead.
type previewContractUC struct {
	templateContractUC
	imageID     uuid.UUID
	previewErr  error
	previewedID uuid.UUID
	previewed   emailUseCase.PreviewInput
	// missingEventID makes EventEmailBrandLogo report ErrEventNotFound.
	missingEventID uuid.UUID
}

func (u *previewContractUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return eventManagerModel.ErrEventManagementForbidden.Err()
}

func (u *previewContractUC) PreviewEventEmail(_ context.Context, eventID uuid.UUID, in emailUseCase.PreviewInput) (emailUseCase.PreviewOutput, error) {
	u.previewedID, u.previewed = eventID, in
	if u.previewErr != nil {
		return emailUseCase.PreviewOutput{}, u.previewErr
	}
	return emailUseCase.PreviewOutput{
		Subject:   "S:" + in.Subject,
		Preheader: "P:" + in.Preheader,
		HTML:      "<p>" + u.imageID.String() + "</p>",
	}, nil
}

func (u *previewContractUC) StreamEventEmailImage(_ context.Context, _, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	if fileID != u.imageID {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return io.NopCloser(strings.NewReader("PNG")), mediaModel.File{ID: fileID, ContentType: "image/png", SizeBytes: 3}, nil
}

func (u *previewContractUC) EventEmailBrandLogo(_ context.Context, eventID uuid.UUID) ([]byte, string, error) {
	if eventID == u.missingEventID {
		return nil, "", eventModel.ErrEventNotFound.Err()
	}
	return []byte("LOGO"), "image/png", nil
}

func TestEventEmailBrandLogo_UnknownEventIs404(t *testing.T) {
	actor, missing := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &previewContractUC{missingEventID: missing}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+missing.String()+"/manage/notification-templates/email/brand/logo", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404 body=%s", w.Code, w.Body.String())
	}
}

func TestEventEmailPreviewHTTPContract(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &previewContractUC{imageID: uuid.Must(uuid.NewV7())}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/notification-templates/email"

	got := eventRequest(t, router, http.MethodPost, base+"/preview",
		`{"NotificationType":"participant.enrolled","Subject":"Hi","Preheader":"pre","Body":[{"type":"logo"}],"Styling":{},"Values":{"event_name":"X"}}`)
	if u.previewedID != eventID || u.previewed.NotificationType != "participant.enrolled" || u.previewed.Values["event_name"] != "X" {
		t.Fatalf("preview call mismatch: %s %+v", u.previewedID, u.previewed)
	}
	var data struct{ Subject, Preheader, HTML string }
	if err := json.Unmarshal(got["Data"], &data); err != nil {
		t.Fatalf("decode %s: %v", got["Data"], err)
	}
	wantHTML := "<p>" + u.imageID.String() + "</p>"
	if data.Subject != "S:Hi" || data.Preheader != "P:pre" || data.HTML != wantHTML {
		t.Fatalf("preview data=%+v want HTML %q", data, wantHTML)
	}

	for path, contentType := range map[string]string{
		base + "/images/" + u.imageID.String(): "image/png",
		base + "/brand/logo":                   "image/png",
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != contentType || w.Body.Len() == 0 {
			t.Fatalf("GET %s: status=%d type=%q len=%d", path, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, base+"/images/"+uuid.Must(uuid.NewV7()).String(), nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("unreferenced image: status=%d want 404", w.Code)
	}
}

func TestEventEmailPreview_NotEventScopedIs400(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &previewContractUC{previewErr: notificationModel.ErrTemplateTypeNotEventScoped.Err()}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/events/"+eventID.String()+"/manage/notification-templates/email/preview",
		strings.NewReader(`{"NotificationType":"event.manager.assigned","Body":[]}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400 body=%s", w.Code, w.Body.String())
	}
}
