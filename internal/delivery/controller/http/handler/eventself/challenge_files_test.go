package eventself

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

func TestChallengeAttachmentDownloadUsesAuthenticatedIdentityAndSafeHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	challengeID, fileID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	filename := "завдання.txt"
	h := NewEventSelfAPIHandler(fakeUseCase{attachment: func(_ context.Context, gotEvent, gotUser, gotChallenge, gotFile uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
		if gotEvent != eventID || gotUser != userID || gotChallenge != challengeID || gotFile != fileID {
			t.Fatalf("unexpected attachment identity: %s %s %s %s", gotEvent, gotUser, gotChallenge, gotFile)
		}
		return io.NopCloser(strings.NewReader("file")), mediaModel.File{Name: filename, ContentType: "text/plain", SizeBytes: 4}, nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/teams/challenges/"+challengeID.String()+"/files/"+fileID.String(), nil)
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}, {Key: "challengeID", Value: challengeID.String()}, {Key: "fileID", Value: fileID.String()}}
	h.downloadChallengeAttachment(ctx)
	if w.Code != http.StatusOK || w.Body.String() != "file" {
		t.Fatalf("download response: %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "attachment") || !strings.Contains(got, "filename*") {
		t.Fatalf("unsafe filename header: %q", got)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("missing nosniff header: %#v", w.Header())
	}
	if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Fatalf("missing private attachment headers: %#v", w.Header())
	}
}
