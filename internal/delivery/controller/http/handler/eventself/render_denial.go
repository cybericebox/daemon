package eventself

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

const renderDenialMinimum = 100 * time.Millisecond

var missingRenderEventID = uuid.FromStringOrNil("00000000-0000-4000-8000-000000000000")

func waitForRenderDenial(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Buffer only these small JSON responses: no denial headers/body leave the
// process until the monotonic minimum window has elapsed. This is a bounded
// local defense, not a claim of equal network/DB tail-latency distributions.
func (h *EventRenderHandler) noStore(ctx *gin.Context) {
	start := h.now()
	original := ctx.Writer
	buffered := &renderResponseBuffer{ResponseWriter: original, headers: original.Header().Clone(), status: http.StatusOK, size: -1}
	ctx.Writer = buffered
	defer func() { ctx.Writer = original }()
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Header("Vary", "Origin, Authorization, Cookie")
	ctx.Next()
	status := response.FinalStatus(ctx)
	if status == http.StatusNotFound {
		buffered.body.Reset()
		buffered.size = 0
		buffered.status = status
		buffered.headers.Del("Content-Type")
		buffered.headers.Del("Content-Length")
		ctx.Set(response.ErrorCtxKey, nil)
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusNotFound {
		if remaining := renderDenialMinimum - h.now().Sub(start); remaining > 0 {
			if h.wait(ctx.Request.Context(), remaining) != nil {
				return
			}
		}
	}
	if ctx.Request.Context().Err() != nil {
		return
	}
	for key := range original.Header() {
		original.Header().Del(key)
	}
	for key, values := range buffered.headers {
		original.Header()[key] = values
	}
	ctx.Writer = original
	// The outer error handler owns application errors and has not rendered yet.
	if status != buffered.status {
		return
	}
	original.WriteHeader(buffered.status)
	if buffered.body.Len() > 0 {
		_, _ = original.Write(buffered.body.Bytes())
	} else {
		original.WriteHeaderNow()
	}
}

type renderResponseBuffer struct {
	gin.ResponseWriter
	headers      http.Header
	body         bytes.Buffer
	status, size int
}

func (b *renderResponseBuffer) Header() http.Header { return b.headers }
func (b *renderResponseBuffer) WriteHeader(code int) {
	if !b.Written() {
		b.status = code
	}
}
func (b *renderResponseBuffer) WriteHeaderNow() {
	if !b.Written() {
		b.size = 0
	}
}
func (b *renderResponseBuffer) Write(value []byte) (int, error) {
	b.WriteHeaderNow()
	n, err := b.body.Write(value)
	b.size += n
	return n, err
}
func (b *renderResponseBuffer) WriteString(value string) (int, error) { return b.Write([]byte(value)) }
func (b *renderResponseBuffer) Status() int                           { return b.status }
func (b *renderResponseBuffer) Size() int                             { return b.size }
func (b *renderResponseBuffer) Written() bool                         { return b.size >= 0 }
func (b *renderResponseBuffer) Flush()                                { b.WriteHeaderNow() }

// The missing-event branch performs the same current membership lookup using a
// fixed non-authorizing ID. No result for that ID can create a tenant or grant.
func (h *EventRenderHandler) resolveGrantTenant(ctx *gin.Context) {
	origin := ctx.GetHeader("Origin")
	parsed, err := url.Parse(origin)
	if err != nil {
		response.AbortWithNotFound(ctx)
		return
	}
	tag, ok := h.hosts.EventTag(parsed.Hostname())
	if !ok || origin != strings.TrimSuffix(h.hosts.EventURL(tag), "/") {
		response.AbortWithNotFound(ctx)
		return
	}
	identity, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	tenant, lookupErr := h.uc.ResolveEventByTag(ctx.Request.Context(), tag, time.Now().UTC())
	membershipID := tenant.EventID
	if lookupErr != nil {
		membershipID = missingRenderEventID
	}
	membershipErr := h.uc.RequireReadEvent(ctx.Request.Context(), membershipID, identity.UserID)
	if lookupErr != nil && !errors.Is(lookupErr, eventModel.ErrEventNotFound.Err()) {
		h.readError(ctx, lookupErr)
		return
	}
	if membershipErr != nil && !errors.Is(membershipErr, eventManagerModel.ErrEventManagementForbidden.Err()) {
		h.readError(ctx, membershipErr)
		return
	}
	if lookupErr != nil || membershipErr != nil || tenant.Public {
		response.AbortWithNotFound(ctx)
		return
	}
	ctx.Request = ctx.Request.WithContext(middleware.ContextWithEventTenant(ctx.Request.Context(), middleware.EventTenant{EventID: tenant.EventID, Tag: tenant.Tag, Public: false, AvailableFrom: tenant.AvailableFrom, ArchiveAt: tenant.ArchiveAt}))
	ctx.Next()
}
