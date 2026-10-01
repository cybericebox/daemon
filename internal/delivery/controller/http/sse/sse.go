// Package sse prepares long-lived Server-Sent Events responses.
package sse

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// maxLifetime bounds one stream. When it runs out, the server ends the
// stream cleanly and the client reconnects with Last-Event-ID.
var maxLifetime = 30 * time.Minute

// Open prepares ctx for an event stream. The server WriteTimeout would close
// the stream after a few seconds, so Open lifts the write deadline for this
// response only. The returned context ends when the client leaves or the
// stream reaches its maximum lifetime; the caller must call cancel.
func Open(ctx *gin.Context) (http.Flusher, context.Context, context.CancelFunc, error) {
	flusher, ok := ctx.Writer.(http.Flusher)
	if !ok {
		return nil, nil, nil, fmt.Errorf("streaming is unavailable")
	}
	// Writers without a connection (test recorders) have no deadline to lift.
	if err := http.NewResponseController(ctx.Writer).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, nil, nil, fmt.Errorf("lift stream write deadline: %w", err)
	}
	streamCtx, cancel := context.WithTimeout(ctx.Request.Context(), maxLifetime)
	return flusher, streamCtx, cancel, nil
}

// HeartbeatInterval is how often an idle stream proves it is alive.
const HeartbeatInterval = 15 * time.Second

// Heartbeat writes a named "heartbeat" event. Unlike an SSE comment, the
// browser delivers it to the page, so the page can show when the data was
// last confirmed fresh; clients that do not listen for it ignore it.
func Heartbeat(w io.Writer, flusher http.Flusher) {
	_, _ = fmt.Fprint(w, "event: heartbeat\ndata: {}\n\n")
	flusher.Flush()
}

// WriteHeaders starts the event-stream response.
func WriteHeaders(ctx *gin.Context) {
	ctx.Header("Content-Type", "text/event-stream")
	ctx.Header("Cache-Control", "no-cache")
	ctx.Header("Connection", "keep-alive")
	ctx.Status(http.StatusOK)
	// Send the headers now: EventSource only fires "open" once it sees them, so a
	// quiet stream would otherwise look like it is still connecting.
	ctx.Writer.Flush()
}
