// Package errjournal is the HTTP capture point of the platform error journal: one middleware that sees every
// request end. It records 5xx, panics, 403 refused by a route permission (with that permission; a business refusal of a use case is routine and not recorded) (429 is protection, not a fault, and is not recorded), counts 404s
// per route template (unmatched paths in one counter, never stored) and gives every request an id. 401 is not
// recorded: expired sessions and sign-in failures are noise. No client address is ever read.
package errjournal

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"runtime/debug"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// HeaderRequestID carries the request id in and out.
const HeaderRequestID = "X-Request-ID"

const (
	keyRequestID  = "errjournal.requestID"
	keyPermission = "errjournal.permission"

	// unmatched is the Source of an error on a request that matched no route.
	unmatched = "(unmatched)"
)

// Sink is what the middleware reports to: the journal use case.
type Sink interface {
	Report(errorJournal.Event)
	CountNotFound(route string)
}

var reSaneRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// SetPermission names the permission that refused the request (RequirePermission calls it before it aborts 403).
func SetPermission(c *gin.Context, permission string) { c.Set(keyPermission, permission) }

// RequestID is the id of the request, empty before the middleware ran.
func RequestID(c *gin.Context) string {
	if v, ok := c.Get(keyRequestID); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// Middleware captures the outcome of every request. Place it right inside the recovery middleware and outside the
// error handler, so the status it reads is the one the client got. sink may be nil (nothing is captured, ids are
// still assigned).
func Middleware(sink Sink) gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := incomingRequestID(c)
		c.Set(keyRequestID, rid)
		c.Header(HeaderRequestID, rid)
		if sink == nil {
			c.Next()
			return
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				if !isBenignPanic(recovered) {
					sink.Report(panicEvent(c, rid, recovered))
				}
				panic(recovered) // the recovery middleware outside answers the client
			}
		}()
		c.Next()
		capture(c, sink, rid)
	}
}

func incomingRequestID(c *gin.Context) string {
	if v := c.GetHeader(HeaderRequestID); reSaneRequestID.MatchString(v) {
		return v
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "unknown"
	}
	return id.String()
}

func capture(c *gin.Context, sink Sink, rid string) {
	status := c.Writer.Status()
	route := c.FullPath()
	switch {
	case status == http.StatusNotFound:
		sink.CountNotFound(route) // empty route: the path matched nothing
	case status >= http.StatusInternalServerError:
		e := base(c, rid, route, status)
		e.Kind = errorJournal.KindHTTP5xx
		e.Message = errorText(c, fmt.Sprintf("HTTP %d without an error", status))
		sink.Report(e)
	case status == http.StatusForbidden:
		// Only a refused route permission is a signal (a wrong permission, probing). A business refusal of a
		// handler or use case (a viewer's write, a non-captain's disband, a participant not yet joined) is routine.
		permission := stringKey(c, keyPermission)
		if permission == "" {
			return
		}
		e := base(c, rid, route, status)
		e.Kind = errorJournal.KindHTTP403
		e.Permission = permission
		e.Message = errorText(c, "forbidden")
		sink.Report(e)
		// 429 is protection working (a rate limit refusing a flood), not a fault: it is not recorded here. The
		// organizers see it as the rejected attempts of the integrity analytics.
	}
}

func base(c *gin.Context, rid, route string, status int) errorJournal.Event {
	e := errorJournal.Event{
		Source: sourceOf(route), Route: sourceOf(route), Method: c.Request.Method, HTTPStatus: status, RequestID: rid,
	}
	if claims, ok := rbac.CurrentUserSessionFromContext(c.Request.Context()); ok {
		id := claims.UserID
		e.UserID = &id
		e.Role = string(claims.Role)
	}
	return e
}

func panicEvent(c *gin.Context, rid string, recovered any) errorJournal.Event {
	e := base(c, rid, c.FullPath(), http.StatusInternalServerError)
	e.Kind = errorJournal.KindPanic
	e.Message = fmt.Sprint(recovered)
	e.Stack = string(debug.Stack())
	return e
}

func sourceOf(route string) string {
	if route == "" {
		return unmatched
	}
	return route
}

// errorText is the error chain of the request, as the error handler stored it.
func errorText(c *gin.Context, fallback string) string {
	if v, ok := c.Get(response.ErrorCtxKey); ok {
		if err, ok := v.(error); ok && err != nil {
			return err.Error()
		}
	}
	if len(c.Errors) > 0 {
		return c.Errors.String()
	}
	return fallback
}

func stringKey(c *gin.Context, key string) string {
	if v, ok := c.Get(key); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// isBenignPanic: a client that went away mid-response, and the abort the net/http server uses on purpose.
func isBenignPanic(recovered any) bool {
	err, ok := recovered.(error)
	if !ok {
		return false
	}
	if errors.Is(err, http.ErrAbortHandler) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			msg := strings.ToLower(sysErr.Error())
			return strings.Contains(msg, "broken pipe") || strings.Contains(msg, "connection reset by peer")
		}
		return errors.Is(opErr.Err, syscall.EPIPE) || errors.Is(opErr.Err, syscall.ECONNRESET)
	}
	return false
}
