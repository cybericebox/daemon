package eventself

import (
	"context"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// presenceInterval is how often one user's presence on one event is written.
const presenceInterval = time.Minute

// presenceToucher is the optional use case port that records when a user was
// last online on an event.
type presenceToucher interface {
	TouchParticipantPresence(ctx context.Context, eventID, userID uuid.UUID) error
}

type presenceKey struct{ eventID, userID uuid.UUID }

// presenceThrottle remembers when each (event, user) pair was last written, so
// a burst of requests costs one database write a minute.
type presenceThrottle struct {
	mu   sync.Mutex
	last map[presenceKey]time.Time
}

// allow reports whether the pair is due a write at now and marks it written.
func (t *presenceThrottle) allow(key presenceKey, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if at, ok := t.last[key]; ok && now.Sub(at) < presenceInterval {
		return false
	}
	if t.last == nil {
		t.last = map[presenceKey]time.Time{}
	}
	if len(t.last) >= 4096 {
		for k, at := range t.last {
			if now.Sub(at) >= presenceInterval {
				delete(t.last, k)
			}
		}
	}
	t.last[key] = now
	return true
}

// touchPresence records, off the request path, that the signed-in caller was
// online on the event the route belongs to (the tenant event or the :id
// path event). Anonymous calls and failures never affect the request.
func (h *Handler) touchPresence(ctx *gin.Context) {
	toucher, ok := h.useCase.(presenceToucher)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		return
	}
	var eventID uuid.UUID
	if tenant, found := middleware.EventTenantFromContext(ctx.Request.Context()); found {
		eventID = tenant.EventID
	} else if id, err := uuid.FromString(ctx.Param("id")); err == nil {
		eventID = id
	} else {
		return
	}
	if !h.presence.allow(presenceKey{eventID: eventID, userID: claims.UserID}, time.Now()) {
		return
	}
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := toucher.TouchParticipantPresence(c, eventID, claims.UserID); err != nil {
			log.Debug().Err(err).Str("eventID", eventID.String()).Msg("Failed to record event presence")
		}
	}()
}
