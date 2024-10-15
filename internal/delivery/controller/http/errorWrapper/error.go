package errorWrapper

import (
	"github.com/cybericebox/daemon/internal/appError"
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

	log.Debug().Err(errUnwrapped).Str("url", ctx.Request.URL.Path).Interface("context", ctx.Keys).Msg("Error")

	if errUnwrapped.Code().IsInternal() {
		log.Error().Err(errUnwrapped).Str("url", ctx.Request.URL.Path).Interface("context", ctx.Keys).Msg("Internal server error")
		errUnwrapped = appError.ErrInternal.WithCode(errUnwrapped.Code()).WithMessage("Internal server error").Err()
	}

	response.AbortWithStatus(ctx, errUnwrapped)
}
