package eventself

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestEventInfoResponsesIncludeTheSavedFavicon(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.FromString("01900000-0000-7000-8000-000000000001"))
	faviconURL := "/api/events/01900000-0000-7000-8000-000000000001/favicon/01900000-0000-7000-8000-000000000002"
	view := eventUseCase.EventInfoView{EventID: eventID, Name: "Event", Status: eventModel.LifecyclePublished, FaviconURL: faviconURL}
	h := NewEventSelfAPIHandler(fakeUseCase{info: func(context.Context, uuid.UUID) (eventUseCase.EventInfoView, error) { return view, nil }}, nil)
	for _, tc := range []struct {
		path    string
		handler gin.HandlerFunc
	}{
		{"public-info", h.publicInfo},
		{"info", h.info},
	} {
		t.Run(tc.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/events/self/"+tc.path, nil)
			request = request.WithContext(middleware.ContextWithEventTenant(request.Context(), middleware.EventTenant{EventID: eventID}))
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = request
			tc.handler(ctx)
			require.Equal(t, http.StatusOK, w.Code)
			var body struct {
				Data map[string]any `json:"Data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			require.Equal(t, faviconURL, body.Data["FaviconURL"], "the metadata and manager reads need the saved icon URL")
		})
	}
}
