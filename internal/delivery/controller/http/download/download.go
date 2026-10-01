// Package download prepares responses that stream large bodies.
package download

import (
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
