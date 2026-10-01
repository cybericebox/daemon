package eventself

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

type labLinkFake struct {
	fakeUseCase
	calls   [][3]uuid.UUID
	devices []string
}

func (f *labLinkFake) OpenOwnLabLink(_ context.Context, eventID, userID, challengeID uuid.UUID, device string, port int32) (labaccess.Link, error) {
	f.calls = append(f.calls, [3]uuid.UUID{eventID, userID, challengeID})
	f.devices = append(f.devices, device)
	return labaccess.Link{URL: "https://web-abc123.challenges.example.com/_auth?t=jwt", Token: "jwt", ExpiresAt: time.Date(2026, 9, 30, 13, 0, 0, 0, time.UTC)}, nil
}

func linkContext(eventID, userID, challengeID uuid.UUID, body string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	request := httptest.NewRequest(http.MethodPost, "/api/events/"+eventID.String()+"/teams/challenges/"+challengeID.String()+"/lab/link", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}, {Key: "challengeID", Value: challengeID.String()}}
	return ctx, w
}

func TestOpenLabLinkReturnsTheLinkAndSetsNoCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	fake := &labLinkFake{}
	h := NewEventSelfAPIHandler(fake, nil)
	ctx, w := linkContext(eventID, userID, challengeID, `{"Device":"web","Port":80}`)
	h.openLabLink(ctx)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if len(fake.calls) != 1 || fake.calls[0] != [3]uuid.UUID{eventID, userID, challengeID} || fake.devices[0] != "web" {
		t.Fatalf("calls = %v %v", fake.calls, fake.devices)
	}
	if !strings.Contains(w.Body.String(), `"url":"https://web-abc123.challenges.example.com/_auth?t=jwt"`) || !strings.Contains(w.Body.String(), `"expires_at"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 || w.Header().Get("Set-Cookie") != "" {
		t.Fatalf("the platform sets no lab cookie: %+v", w.Result().Cookies())
	}
	if w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("Cache-Control = %q", w.Header().Get("Cache-Control"))
	}
}

func TestOpenLabLinkRequiresADevice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &labLinkFake{}
	h := NewEventSelfAPIHandler(fake, nil)
	ctx, w := linkContext(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), `{}`)
	h.openLabLink(ctx)
	if _, failed := ctx.Get(response.ErrorCtxKey); (!failed && w.Code == http.StatusOK) || len(fake.calls) != 0 {
		t.Fatalf("a link without a device must be refused: code=%d failed=%v", w.Code, failed)
	}
}

func TestOpenLabLinkIsUnavailableWhenTheUseCaseCannotIssue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewEventSelfAPIHandler(fakeUseCase{}, nil)
	ctx, w := linkContext(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), `{"Device":"web","Port":80}`)
	h.openLabLink(ctx)
	// The error middleware turns the stored error into the response.
	if _, failed := ctx.Get(response.ErrorCtxKey); !failed || len(w.Result().Cookies()) != 0 {
		t.Fatalf("failed = %v cookies = %v", failed, w.Result().Cookies())
	}
}
