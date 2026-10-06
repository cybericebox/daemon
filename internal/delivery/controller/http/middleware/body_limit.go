package middleware

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

const originalBodyKey = "http.originalBody"

// BodyLimit caps every request body at max bytes: a read past it fails and the handler gets a 4xx, so no
// route buffers an unbounded body. A route that really takes a larger body (an upload) raises its own cap
// with LimitBody.
func BodyLimit(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil && c.Request.Body != http.NoBody {
			c.Set(originalBodyKey, c.Request.Body)
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		}
		c.Next()
	}
}

// LimitBody sets the cap of this request's body to max bytes, above or below the global one: an upload
// route states its own limit here instead of wrapping the already capped body.
func LimitBody(c *gin.Context, max int64) {
	body := c.Request.Body
	if original, ok := c.Get(originalBodyKey); ok {
		if rc, ok := original.(io.ReadCloser); ok {
			body = rc
		}
	}
	if body == nil || body == http.NoBody {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, body, max)
}
