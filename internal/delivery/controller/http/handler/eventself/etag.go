package eventself

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// abortWithETagData answers like response.AbortWithData, tagged with a hash
// of the data: a poll whose If-None-Match still matches gets an empty 304.
func abortWithETagData(ctx *gin.Context, data any) {
	response.AbortWithETagData(ctx, data, 0)
}
