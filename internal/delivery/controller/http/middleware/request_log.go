package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// ForMode is the logger + recovery pair for a gin mode (set by
// config.SetupLogger from ENV). Release (stage, production) logs everything
// as zerolog JSON: RequestLogger and Recovery, no gin text output. Debug
// (development) keeps gin's own coloured request lines and panic dump,
// exactly what gin.Default attached in the old daemon. The router is built with gin.New, so
// neither mode triggers gin's "Logger and Recovery already attached" warning.
func ForMode(mode string) []gin.HandlerFunc {
	if mode == gin.ReleaseMode {
		return []gin.HandlerFunc{RequestLogger(), Recovery()}
	}
	return []gin.HandlerFunc{gin.LoggerWithConfig(gin.LoggerConfig{Formatter: consoleLine}), gin.Recovery()}
}

// consoleLine is gin's coloured request line without the query string and the client address: the query
// carries tokens (setup, invitation, live screen) and OAuth codes, and an address is not logged.
func consoleLine(p gin.LogFormatterParams) string {
	path, _, _ := strings.Cut(p.Path, "?")
	return fmt.Sprintf("[GIN] %s |%s %3d %s| %13v | %s %-7s %s\n%s",
		p.TimeStamp.Format("2006/01/02 - 15:04:05"),
		p.StatusCodeColor(), p.StatusCode, p.ResetColor(),
		p.Latency, p.MethodColor(), p.Method+p.ResetColor(), path, p.ErrorMessage)
}

// RequestLogger writes one zerolog line per request: info for success, warn
// for 4xx, error for 5xx, with gin's collected errors when there are any. Each
// line names the route template (empty for a path no route matches) and
// whether the caller was anonymous. The refusals a flood produces in bulk (401,
// 429 and the overload 503) are sampled, see refusalSampler.
func RequestLogger() gin.HandlerFunc {
	return requestLogger(time.Now)
}

func requestLogger(now func() time.Time) gin.HandlerFunc {
	sampler := &refusalSampler{now: now, last: map[int]time.Time{}, suppressed: map[int]int{}}
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		queryKeys := queryKeyNames(c.Request.URL.Query())

		c.Next()

		status := c.Writer.Status()
		level := levelForStatus(status)
		suppressed := 0
		if sampled(c, status) {
			var write bool
			if write, suppressed = sampler.admit(status); !write {
				return
			}
			level = zerolog.WarnLevel
		}
		_, signedIn := rbac.CurrentUserSessionFromContext(c.Request.Context())
		event := log.WithLevel(level).
			Str("method", c.Request.Method).
			Str("path", path).
			Str("route", c.FullPath()).
			Bool("anon", !signedIn).
			Int("status", status).
			Dur("latency", time.Since(start)).
			Int("size", c.Writer.Size()).
			Str("user_agent", c.Request.UserAgent())
		// Only the names of the query parameters are logged: their values carry tokens and codes.
		if len(queryKeys) > 0 {
			event = event.Strs("query_keys", queryKeys)
		}
		if len(c.Errors) > 0 {
			event = event.Str("errors", c.Errors.String())
		}
		if suppressed > 0 {
			event = event.Int("suppressed", suppressed)
		}
		event.Msg("HTTP request")
	}
}

// sampled says whether the line of a request is one a flood produces in bulk: a 401, a 429 or an overload
// 503 (any other 503 is a fault and always logged).
func sampled(c *gin.Context, status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusTooManyRequests:
		return true
	case http.StatusServiceUnavailable:
		return response.IsOverload(c)
	}
	return false
}

// refusalSampler writes at most one line per status per second: the first; the rest are counted and the
// count goes on the next written line of that status.
type refusalSampler struct {
	mu         sync.Mutex
	now        func() time.Time
	last       map[int]time.Time
	suppressed map[int]int
}

// admit says whether the line of status is written, and how many lines of status were dropped before it.
func (s *refusalSampler) admit(status int) (bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if last, ok := s.last[status]; ok && now.Sub(last) < time.Second {
		s.suppressed[status]++
		return false, 0
	}
	s.last[status] = now
	dropped := s.suppressed[status]
	delete(s.suppressed, status)
	return true, dropped
}

// queryKeyNames lists the parameter names of a query, sorted.
func queryKeyNames(values url.Values) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func levelForStatus(status int) zerolog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return zerolog.ErrorLevel
	case status >= http.StatusBadRequest:
		return zerolog.WarnLevel
	default:
		return zerolog.InfoLevel
	}
}

// Recovery turns a handler panic into a bare 500 and logs it with the stack.
// gin's own writer is discarded so the panic is logged once, through zerolog.
// A broken client connection is not a server fault: gin aborts it without
// calling the handler, as gin.Recovery does.
func Recovery() gin.HandlerFunc {
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		log.Error().
			Interface("panic", recovered).
			Str("method", c.Request.Method).
			Str("path", c.Request.URL.Path).
			Bytes("stack", debug.Stack()).
			Msg("Recovered from panic")
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}
