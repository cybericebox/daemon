package middleware

import (
	"context"
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
