package middleware

import (
	"io"
	"net/http"
	"runtime/debug"
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
	return []gin.HandlerFunc{gin.Logger(), gin.Recovery()}
}

// RequestLogger writes one zerolog line per request: info for success, warn
// for 4xx, error for 5xx, with gin's collected errors when there are any.
func RequestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		status := c.Writer.Status()
		event := log.WithLevel(levelForStatus(status)).
			Str("method", c.Request.Method).
			Str("path", path).
			Int("status", status).
			Dur("latency", time.Since(start)).
			Str("ip", c.ClientIP()).
			Int("size", c.Writer.Size()).
			Str("user_agent", c.Request.UserAgent())
		if query != "" {
			event = event.Str("query", query)
		}
		if len(c.Errors) > 0 {
			event = event.Str("errors", c.Errors.String())
		}
		event.Msg("HTTP request")
	}
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
