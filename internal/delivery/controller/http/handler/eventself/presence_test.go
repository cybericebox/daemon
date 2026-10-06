package eventself

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type presenceCall struct{ eventID, userID uuid.UUID }

// presenceUseCase embeds the port so only the touch is implemented.
type presenceUseCase struct {
	IUseCase
	mu    sync.Mutex
	calls []presenceCall
	done  chan struct{}
}

func (p *presenceUseCase) TouchParticipantPresence(_ context.Context, eventID, userID uuid.UUID) error {
	p.mu.Lock()
	p.calls = append(p.calls, presenceCall{eventID, userID})
	p.mu.Unlock()
	p.done <- struct{}{}
	return nil
}

func (p *presenceUseCase) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.calls)
}

func presenceRouter(h *Handler, claims *rbac.Claims, tenant *middleware.EventTenant) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		ctx := c.Request.Context()
		if claims != nil {
			ctx = rbac.ContextWithCurrentUserSession(ctx, *claims)
		}
		if tenant != nil {
			ctx = middleware.ContextWithEventTenant(ctx, *tenant)
		}
		c.Request = c.Request.WithContext(ctx)
	})
	r.GET("/events/:id/teams/mine", h.touchPresence, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/events/self/info", h.touchPresence, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	return r
}

func hit(r *gin.Engine, path string) int {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Code
}

func waitTouch(t *testing.T, uc *presenceUseCase) {
	t.Helper()
	select {
	case <-uc.done:
	case <-time.After(2 * time.Second):
		t.Fatal("presence was not recorded")
	}
}

func TestTouchPresenceRecordsPathEventOncePerMinute(t *testing.T) {
	uc := &presenceUseCase{done: make(chan struct{}, 4)}
	h := &Handler{useCase: uc}
	user, event := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	r := presenceRouter(h, &rbac.Claims{UserID: user}, nil)

	for i := 0; i < 3; i++ {
		if code := hit(r, "/events/"+event.String()+"/teams/mine"); code != http.StatusNoContent {
			t.Fatalf("request passes through: %d", code)
		}
	}
	waitTouch(t, uc)
	time.Sleep(50 * time.Millisecond)
	if uc.count() != 1 || uc.calls[0] != (presenceCall{event, user}) {
		t.Fatalf("a burst is one touch of the path event: %+v", uc.calls)
	}

	// Another event is its own pair.
	other := uuid.Must(uuid.NewV7())
	hit(r, "/events/"+other.String()+"/teams/mine")
	waitTouch(t, uc)
	if uc.count() != 2 || uc.calls[1].eventID != other {
		t.Fatalf("second event: %+v", uc.calls)
	}
}

func TestTouchPresenceUsesTenantEventAndSkipsAnonymous(t *testing.T) {
	uc := &presenceUseCase{done: make(chan struct{}, 4)}
	h := &Handler{useCase: uc}
	user, event := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	anonymous := presenceRouter(h, nil, &middleware.EventTenant{EventID: event})
	if code := hit(anonymous, "/events/self/info"); code != http.StatusNoContent {
		t.Fatalf("anonymous passes through: %d", code)
	}
	time.Sleep(50 * time.Millisecond)
	if uc.count() != 0 {
		t.Fatalf("anonymous callers are not recorded: %+v", uc.calls)
	}

	signedIn := presenceRouter(h, &rbac.Claims{UserID: user}, &middleware.EventTenant{EventID: event})
	hit(signedIn, "/events/self/info")
	waitTouch(t, uc)
	if uc.count() != 1 || uc.calls[0] != (presenceCall{event, user}) {
		t.Fatalf("tenant event: %+v", uc.calls)
	}
}

func TestTouchPresenceIgnoresUseCaseWithoutToucher(t *testing.T) {
	h := &Handler{useCase: nil}
	r := presenceRouter(h, &rbac.Claims{UserID: uuid.Must(uuid.NewV7())}, nil)
	if code := hit(r, "/events/"+uuid.Must(uuid.NewV7()).String()+"/teams/mine"); code != http.StatusNoContent {
		t.Fatalf("code = %d", code)
	}
}

func TestPresenceThrottlePrunesStaleEntries(t *testing.T) {
	var th presenceThrottle
	now := time.Now()
	for i := 0; i < 4096; i++ {
		th.allow(presenceKey{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}, now)
	}
	if !th.allow(presenceKey{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}, now.Add(2*presenceInterval)) {
		t.Fatal("a new pair is allowed")
	}
	if len(th.last) != 1 {
		t.Fatalf("stale entries are pruned, left %d", len(th.last))
	}
}

// L17: the throttle keys come from the caller (the event id of the URL); the map must not follow them without bound.
func TestPresenceThrottleStaysBounded(t *testing.T) {
	var th presenceThrottle
	now := time.Now()
	user := uuid.Must(uuid.NewV7())
	for i := 0; i < 5*maxPresenceKeys; i++ {
		th.allow(presenceKey{eventID: uuid.Must(uuid.NewV7()), userID: user}, now)
	}
	if len(th.last) > maxPresenceKeys {
		t.Fatalf("the throttle map grew to %d keys", len(th.last))
	}
}
