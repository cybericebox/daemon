package middleware

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	// uploadMinRate is the slowest link a chunk upload is still given time for.
	uploadMinRate = 256 << 10
	// uploadMinTimeout and uploadMaxTimeout bound the time of one chunk request.
	uploadMinTimeout = time.Minute
	uploadMaxTimeout = 10 * time.Minute
)

// UploadTimeout is the time one request carrying a body of size bytes gets: the size at uploadMinRate, at least a
// minute and at most ten.
func UploadTimeout(size int64) time.Duration {
	d := time.Duration(size/uploadMinRate) * time.Second
	return min(max(d, uploadMinTimeout), uploadMaxTimeout)
}

// ExtendUploadDeadlines gives this request, and only it, the time to receive a body of size bytes and answer. The
// server READ_TIMEOUT and WRITE_TIMEOUT are sized for ordinary requests and cut a chunk of a slow link off midway
// (on HTTP/1, HTTP/2 and HTTP/3 alike: the HTTP/3 server applies them per request through the same
// ResponseController). A writer without a connection (a test recorder) has no deadline, so a failure to set it is
// ignored.
func ExtendUploadDeadlines(c *gin.Context, size int64) {
	rc := http.NewResponseController(c.Writer)
	deadline := time.Now().Add(UploadTimeout(size))
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
}
