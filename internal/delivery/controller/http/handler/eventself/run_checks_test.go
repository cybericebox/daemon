package eventself

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type runChecksFake struct {
	fakeUseCase

	calls [][3]uuid.UUID
	err   error
}

func (f *runChecksFake) RunOwnChallengeChecks(_ context.Context, eventID, userID, challengeID uuid.UUID) error {
	f.calls = append(f.calls, [3]uuid.UUID{eventID, userID, challengeID})
	return f.err
}

func askForChecks(h *Handler, eventID, userID uuid.UUID, challenge string) *httptest.ResponseRecorder {
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: userID}))
		c.Next()
	})
	r.POST("/api/events/:id/teams/challenges/:challengeID/run-checks", h.runChecks)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/events/"+eventID.String()+"/teams/challenges/"+challenge+"/run-checks", nil))
	return w
}

func TestRunChecksAsksForTheCallersOwnTask(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	fake := &runChecksFake{}
	w := askForChecks(NewEventSelfAPIHandler(fake, nil), eventID, userID, challengeID.String())
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Asked":true`) {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	if len(fake.calls) != 1 || fake.calls[0] != [3]uuid.UUID{eventID, userID, challengeID} {
		t.Fatalf("calls: %v", fake.calls)
	}
	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "no-store") {
		t.Fatalf("Cache-Control = %q", got)
	}
	// Nothing of what is checked appears in the answer.
	for _, word := range []string{"Detail\"", "Expected", "Conditions", "Value"} {
		if strings.Contains(w.Body.String(), word) {
			t.Fatalf("the answer carries %q: %s", word, w.Body.String())
		}
	}
}

func TestRunChecksRejectsABadChallengeID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &runChecksFake{}
	w := askForChecks(NewEventSelfAPIHandler(fake, nil), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "nope")
	if w.Code != http.StatusBadRequest || len(fake.calls) != 0 {
		t.Fatalf("status = %d calls = %v", w.Code, fake.calls)
	}
}

func TestRunChecksTooOftenIs429AndALockedTaskIs409(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"too often":   {exerciseModel.ErrChecksRunTooOften.Err(), http.StatusTooManyRequests},
		"locked":      {exerciseModel.ErrTaskLocked.WithPublicContext("waits_for", "Protocol").Err(), http.StatusConflict},
		"not allowed": {exerciseModel.ErrChecksRunNotAllowed.Err(), http.StatusConflict},
	} {
		t.Run(name, func(t *testing.T) {
			w := askForChecks(NewEventSelfAPIHandler(&runChecksFake{err: tc.err}, nil), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()).String())
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", w.Code, tc.want, w.Body.String())
			}
		})
	}
}
