package errorWrapper

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

func WithErrorHandler(ctx *gin.Context) {
	ctx.Next()

	errFromContext := tools.GetErrorFromContext(ctx)

	if errFromContext == nil {
		return
	}

	errUnwrapped := errFromContext.UnwrapNotInternalError()

	if errUnwrapped.Code().IsInternalError() {
		log.Error().Err(errUnwrapped).Str("url", ctx.Request.URL.Path).Interface("context", ctx.Keys).Msg("Internal server error")
	}

	response.AbortWithCode(ctx, errUnwrapped.Code())
}
