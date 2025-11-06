package errorWrapper

import (
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/tools"
)

func WithErrorHandler(ctx *gin.Context) {
	ctx.Next()

	errFromContext := tools.GetErrorFromContext(ctx)

	if errFromContext == nil {
		return
	}

	errUnwrapped := errFromContext.UnwrapNotInternalError()

	log.Debug().Err(errUnwrapped).Str("url", ctx.Request.URL.Path).Interface("context", ctx.Keys).Msg("Error")

	if errUnwrapped.StatusCode().IsInternal() {
		log.Error().Err(errUnwrapped).Str("url", ctx.Request.URL.Path).Interface(
			"context",
			ctx.Keys,
		).Msg("Internal server error")
		errUnwrapped = err.ErrInternal.WithStatusCode(errUnwrapped.StatusCode()).WithMessage("Internal server error").Err()
	}

	response.AbortWithStatus(ctx, errUnwrapped)
}
