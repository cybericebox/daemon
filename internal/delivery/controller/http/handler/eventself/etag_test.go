package eventself

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestResultsSnapshot_ETagAnswersUnchangedPollsWith304(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	revision := int64(3)
	h := NewEventSelfAPIHandler(fakeUseCase{results: func(context.Context, uuid.UUID, eventUseCase.ResultsAccess) (eventUseCase.ResultsSnapshotView, error) {
		return eventUseCase.ResultsSnapshotView{Revision: revision}, nil
	}}, nil)
	get := func(ifNoneMatch string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(w)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/results", nil)
		if ifNoneMatch != "" {
			ctx.Request.Header.Set("If-None-Match", ifNoneMatch)
		}
		ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
		h.resultsSnapshot(ctx)
		return w
	}

	first := get("")
	tag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || tag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("first poll: %d etag=%q cache=%q", first.Code, tag, first.Header().Get("Cache-Control"))
	}
	if same := get(tag); same.Code != http.StatusNotModified || same.Body.Len() != 0 || same.Header().Get("ETag") != tag {
		t.Fatalf("unchanged poll: %d body=%q", same.Code, same.Body.String())
	}
	if weak := get(`"other", W/` + tag); weak.Code != http.StatusNotModified {
		t.Fatalf("a list with the weak tag must match: %d", weak.Code)
	}
	revision = 4
	changed := get(tag)
	if changed.Code != http.StatusOK || changed.Header().Get("ETag") == tag {
		t.Fatalf("changed results must be sent again: %d etag=%q", changed.Code, changed.Header().Get("ETag"))
	}
}

// A screen link's snapshot is not revalidated: no ETag, and the no-store of
// the screen routes stays.
func TestResultsSnapshot_ScreenLinkGetsNoETag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{results: func(context.Context, uuid.UUID, eventUseCase.ResultsAccess) (eventUseCase.ResultsSnapshotView, error) {
		return eventUseCase.ResultsSnapshotView{Revision: 3}, nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/events/self/live-screen/results", nil)
	ctx.Request = ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), liveScreenTokenKey{}, "token"))
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	ctx.Header("Cache-Control", "private, no-store")
	h.resultsSnapshot(ctx)
	if w.Code != http.StatusOK || w.Header().Get("ETag") != "" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("screen snapshot: %d etag=%q cache=%q", w.Code, w.Header().Get("ETag"), w.Header().Get("Cache-Control"))
	}
}
