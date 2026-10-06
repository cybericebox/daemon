package eventself

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestPublicInfoExposesCountdownPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	view := eventUseCase.EventInfoView{
		EventID: eventID, Name: "Подія", Status: eventModel.LifecyclePublished,
		Theme:     eventConfigModel.DefaultTheme(),
		Countdown: eventConfigModel.CountdownSettings{ShowStart: false, ShowFinish: true, FinishMinutes: 15},
	}
	h := NewEventSelfAPIHandler(fakeUseCase{info: func(context.Context, uuid.UUID) (eventUseCase.EventInfoView, error) { return view, nil }}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/events/self/public-info", nil)
	request = request.WithContext(middleware.ContextWithEventTenant(request.Context(), middleware.EventTenant{EventID: eventID}))
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = request
	h.publicInfo(ctx)
	body := w.Body.String()
	for _, want := range []string{`"ShowStartCountdown":false`, `"ShowFinishCountdown":true`, `"FinishCountdownMinutes":15`} {
		if w.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("public info lacks %s: %d %s", want, w.Code, body)
		}
	}
}
