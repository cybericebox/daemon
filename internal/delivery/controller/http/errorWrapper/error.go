package errorWrapper

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
)

func WithErrorHandler(ctx *gin.Context) {
	ctx.Next()
	errFromContext := tools.GetErrorFromContext(ctx)

	if errFromContext == nil {
		return
	}

	if errFromContext.StatusCode() == 500 {
		response.LogAndAbortWithInternalServerError(ctx, errFromContext)
		return
	}

	response.AbortWithStatus(ctx, errFromContext.StatusCode(), errFromContext.Message)
}
