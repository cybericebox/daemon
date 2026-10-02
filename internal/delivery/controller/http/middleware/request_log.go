package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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
// for 4xx, error for 5xx, with gin's collected errors when there are any.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		queryKeys := queryKeyNames(c.Request.URL.Query())

		c.Next()

		status := c.Writer.Status()
		event := log.WithLevel(levelForStatus(status)).
			Str("method", c.Request.Method).
			Str("path", path).
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
		event.Msg("HTTP request")
	}
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
