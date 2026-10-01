package template

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
)

// ── fakeUseCase: images ──

func (f *fakeUseCase) MaxEmailImageUploadBytes() int64 {
	if f.maxImageUpload > 0 {
		return f.maxImageUpload
	}
	return 10 << 20
}

func (f *fakeUseCase) UploadEmailImage(_ context.Context, r io.Reader, name string, userID uuid.UUID) (mediaModel.File, error) {
	if f.uploadErr != nil {
		return mediaModel.File{}, f.uploadErr
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return mediaModel.File{}, err
	}
	f.uploaded, f.uploadedName, f.uploadedBy = b, name, userID
	return mediaModel.File{ID: f.imageID, Name: name, ContentType: "image/png", SizeBytes: int64(len(b))}, nil
}

func (f *fakeUseCase) StreamEmailImage(_ context.Context, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	if fileID != f.imageID {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return io.NopCloser(bytes.NewReader([]byte("PNGDATA"))), mediaModel.File{ID: fileID, ContentType: "image/png", SizeBytes: 7}, nil
}

func multipartImage(t *testing.T, payload []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "hero.png")
	require.NoError(t, err)
	_, err = part.Write(payload)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return &buf, w.FormDataContentType()
}

func TestUploadEmailImage_ReturnsFileIDAndURL(t *testing.T) {
	uc := &fakeUseCase{imageID: uuid.Must(uuid.NewV7())}
	engine := newEngine(uc)
	body, contentType := multipartImage(t, []byte("raw-image"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/templates/email/images", body)
	req.Header.Set("Content-Type", contentType)
	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var resp struct {
		Data struct {
			FileID uuid.UUID `json:"FileID"`
			Url    string    `json:"Url"`
		} `json:"Data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, uc.imageID, resp.Data.FileID)
	assert.Equal(t, "/api/notifications/templates/email/images/"+uc.imageID.String(), resp.Data.Url)
	assert.Equal(t, []byte("raw-image"), uc.uploaded)
	assert.Equal(t, "hero.png", uc.uploadedName)
	assert.NotEqual(t, uuid.Nil, uc.uploadedBy)
}

func TestUploadEmailImage_MapsDomainError(t *testing.T) {
	uc := &fakeUseCase{uploadErr: notificationModel.ErrTemplateImageInvalid.Err()}
	engine := newEngine(uc)
	body, contentType := multipartImage(t, []byte("<svg/>"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/templates/email/images", body)
	req.Header.Set("Content-Type", contentType)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUploadEmailImage_BodyOverCapIsTooLarge(t *testing.T) {
	// A tiny cap so the body exceeds cap+multipartOverheadSlack cheaply.
	uc := &fakeUseCase{maxImageUpload: 1}
	engine := newEngine(uc)
	body, contentType := multipartImage(t, bytes.Repeat([]byte("x"), multipartOverheadSlack+1024))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/templates/email/images", body)
	req.Header.Set("Content-Type", contentType)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "too large")
	assert.Nil(t, uc.uploaded)
}

func TestUploadEmailImage_DeniedByProt_403(t *testing.T) {
	uc := &fakeUseCase{}
	engine := newEngineWithProt(uc, denyProt{})
	body, contentType := multipartImage(t, []byte("raw"))

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/notifications/templates/email/images", body)
	req.Header.Set("Content-Type", contentType)
	engine.ServeHTTP(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Nil(t, uc.uploaded)
}

func TestStreamEmailImage_ServesInlineImmutable(t *testing.T) {
	uc := &fakeUseCase{imageID: uuid.Must(uuid.NewV7())}
	engine := newEngine(uc)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/notifications/templates/email/images/"+uc.imageID.String(), nil)
	engine.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "PNGDATA", w.Body.String())
	assert.Equal(t, "image/png", w.Header().Get("Content-Type"))
	assert.Equal(t, "inline", w.Header().Get("Content-Disposition"))
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "private, max-age=31536000, immutable", w.Header().Get("Cache-Control"))
}

func TestStreamEmailImage_NotFoundAndBadID(t *testing.T) {
	uc := &fakeUseCase{imageID: uuid.Must(uuid.NewV7())}
	engine := newEngine(uc)

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/templates/email/images/"+uuid.Must(uuid.NewV7()).String(), nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	w = httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/templates/email/images/nope", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestBrandLogo_ServesCrestPNG(t *testing.T) {
	engine := newEngine(&fakeUseCase{})

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/templates/email/brand/logo", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, branding.LogoContentType, w.Header().Get("Content-Type"))
	assert.Equal(t, branding.LogoPNG(), w.Body.Bytes())
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
}

func TestEmailTemplateByIDRoutesStillResolve(t *testing.T) {
	// The static "images" / "brand" segments must not shadow ":id" routes.
	engine := newEngine(&fakeUseCase{})
	id := uuid.Must(uuid.NewV7())

	w := httptest.NewRecorder()
	engine.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/templates/email/"+id.String(), nil))
	assert.Equal(t, http.StatusOK, w.Code)
}
