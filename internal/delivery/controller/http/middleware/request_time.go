package middleware

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

type requestReceivedAtKey struct{}

// CaptureRequestReceivedAt records the request's arrival before routing, body
// parsing, authentication, or database work. Competition tie-breaks must use
// this stable instant rather than the handler's later processing time.
func CaptureRequestReceivedAt() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), requestReceivedAtKey{}, time.Now())
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// RequestReceivedAt returns the middleware-captured arrival instant. The
// fallback keeps direct handler tests and non-HTTP callers well-defined.
func RequestReceivedAt(ctx context.Context) time.Time {
	if value, ok := ctx.Value(requestReceivedAtKey{}).(time.Time); ok && !value.IsZero() {
		return value
	}
	return time.Now()
}

// ReceivedAfterBody is for routes where the arrival instant decides a
// competition (a flag submission: first blood, deadline, tie-break). The
// request is counted as received when its BODY is complete, not when its
// headers were: a client that sends the headers early and holds the answer back
// (the server waits up to the read timeout) would otherwise back-date its
// submission. The body is read here, whole, before the handler, and handed on
// from memory; a read error (too large, broken) is replayed to the handler's
// own bind, which turns it into the usual 4xx.
func ReceivedAfterBody(c *gin.Context) {
	if c.Request.Body != nil && c.Request.Body != http.NoBody {
		data, err := io.ReadAll(c.Request.Body)
		var rest io.Reader = bytes.NewReader(data)
		if err != nil {
			rest = io.MultiReader(rest, failingReader{err})
		}
		c.Request.Body = io.NopCloser(rest)
	}
	ctx := context.WithValue(c.Request.Context(), requestReceivedAtKey{}, time.Now())
	c.Request = c.Request.WithContext(ctx)
	c.Next()
}

type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }
