// Package download prepares responses that stream large bodies.
package download

import (
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// maxDuration bounds one download. The server WriteTimeout is sized for
// ordinary requests and would cut a big export or file off midway.
var maxDuration = 5 * time.Minute

// ExtendDeadline gives this response, and only it, maxDuration from now to
// finish writing. Writers without a connection (test recorders) have no
// deadline, so a failure to set it is ignored.
func ExtendDeadline(ctx *gin.Context) {
	_ = http.NewResponseController(ctx.Writer).SetWriteDeadline(time.Now().Add(maxDuration))
}

// longOperation bounds a route that works on a whole big file on the server (LONG_OPERATION_TIMEOUT).
var longOperation = 15 * time.Minute

// SetLongOperationTimeout sets the longest a long-operation route may take. It is set once at start.
func SetLongOperationTimeout(d time.Duration) { longOperation = d }

// LongOperation is the middleware of a route that does long server-side work on a big file (assembling a
// chunked upload, importing an archive). The server WriteTimeout is sized for ordinary requests: on HTTP/2 it
// resets the stream and cancels the request mid-work. This gives that response, and only it, the long budget.
func LongOperation() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		_ = http.NewResponseController(ctx.Writer).SetWriteDeadline(time.Now().Add(longOperation))
		ctx.Next()
	}
}

// File answers with the body of a stored file and supports Range (resume, chunked fetching): when the reader can
// seek (an object store stream does) the standard library answers 206 and 416 and honours If-Range; a reader that
// cannot seek gets the whole body. The caller sets the headers of the file (type, disposition, caching) first and
// closes the reader.
func File(ctx *gin.Context, reader io.Reader, size int64, contentType string) {
	if contentType != "" {
		ctx.Header("Content-Type", contentType)
	}
	if seeker, ok := reader.(io.ReadSeeker); ok {
		http.ServeContent(ctx.Writer, ctx.Request, "", time.Time{}, seeker)
		return
	}
	ctx.DataFromReader(http.StatusOK, size, contentType, reader, nil)
}
