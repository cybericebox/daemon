package response

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/pkg/err"

	"context"
	"fmt"

	"github.com/cybericebox/daemon/internal/model"
)

const ErrorCtxKey = "error"

func getErrorFromContext(ctx context.Context) err.Error {
	errFromContext := ctx.Value(ErrorCtxKey)

	if errFromContext == nil {
		return nil
	}

	parsedError, ok := errFromContext.(err.Error)
	if !ok {
		errCommonParsed, ok := errFromContext.(error)
		if !ok {
			return model.ErrPlatform.WithMessage(
				fmt.Sprintf(
					"Error in context is not of type error: got [%v]",
					errFromContext,
				),
			).Err()
		}
		return model.ErrPlatform.WithError(errCommonParsed).
			WithMessage(errCommonParsed.Error()).
			Err()
	}

	return parsedError
}

func WithErrorHandler(ctx *gin.Context) {
	ctx.Next()

	errFromContext := getErrorFromContext(ctx)

	if errFromContext == nil {
		return
	}

	errUnwrapped := errFromContext.UnwrapNotInternalError()

	// gin's ctx.Keys is an arbitrary map[string]any whose values are not guaranteed
	// to be JSON-marshalable (e.g. a map[interface{}]interface{} makes zerolog's
	// Interface emit "marshaling error: ..."). Render it with fmt instead, which
	// handles any value type and never fails.
	ctxKeys := fmt.Sprintf("%v", ctx.Keys)

	log.Debug().
		Err(errUnwrapped).
		Str("url", ctx.Request.URL.Path).
		Str("context", ctxKeys).
		Msg("Error")

	if errUnwrapped.StatusCode().IsInternal() {
		log.Error().Err(errUnwrapped).Str("url", ctx.Request.URL.Path).Str(
			"context",
			ctxKeys,
		).Msg("Internal server error")
		errUnwrapped = err.ErrInternal.WithStatusCode(errUnwrapped.StatusCode()).
			WithMessage("Internal server error").
			Err()
	}

	AbortWithStatus(ctx, errUnwrapped)
}
