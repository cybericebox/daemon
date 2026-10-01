package eventself

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func answerFileRequest(fileID string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/events/self/answer-files/"+fileID, nil)
	request = request.WithContext(middleware.ContextWithEventTenant(request.Context(), middleware.EventTenant{EventID: uuid.Must(uuid.NewV7())}))
	return request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7())}))
}

func TestDownloadAnswerFileIsAnAttachment(t *testing.T) {
	fileID := uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{answerFile: func() (io.ReadCloser, eventUseCase.AnswerFileView, error) {
		return io.NopCloser(strings.NewReader("%PDF")), eventUseCase.AnswerFileView{ID: fileID, Name: "резюме \"final\".pdf", Size: 4, ContentType: "application/pdf"}, nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = answerFileRequest(fileID.String())
	ctx.Params = gin.Params{{Key: "fileID", Value: fileID.String()}}
	h.downloadAnswerFile(ctx)
	if w.Code != http.StatusOK || w.Body.String() != "%PDF" {
		t.Fatalf("download: %d %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") {
		t.Fatalf("file must download as an attachment: %q", got)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("download headers: %v", w.Header())
	}
}

func TestDownloadAnswerFileHidesForeignFiles(t *testing.T) {
	fileID := uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{answerFile: func() (io.ReadCloser, eventUseCase.AnswerFileView, error) {
		return nil, eventUseCase.AnswerFileView{}, eventModel.ErrAnswerFileNotFound.Err()
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = answerFileRequest(fileID.String())
	ctx.Params = gin.Params{{Key: "fileID", Value: fileID.String()}}
	h.downloadAnswerFile(ctx)
	got, ok := ctx.Get(response.ErrorCtxKey)
	if !ok || !errors.Is(got.(error), eventModel.ErrAnswerFileNotFound.Err()) || w.Body.Len() != 0 {
		t.Fatalf("a file of someone else must look missing: %v %q", got, w.Body.String())
	}
}
