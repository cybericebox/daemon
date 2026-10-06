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

// FinalStatus is the HTTP status the request will end with. A handler that
// aborted with an error has only stored it: WithErrorHandler writes the
// response after every inner middleware returned, so until then the writer
// still says 200. Code that runs after the handler (the audit log) must use
// this, not ctx.Writer.Status().
func FinalStatus(ctx *gin.Context) int {
	if errFromContext := getErrorFromContext(ctx); errFromContext != nil {
		return errorToWrite(errFromContext).StatusCode().HTTPCode()
	}
	return ctx.Writer.Status()
}

// errorToWrite is the client-facing error: the stored one, with an internal
// error's detail replaced by a generic message.
func errorToWrite(errFromContext err.Error) err.Error {
	errUnwrapped := errFromContext.UnwrapNotInternalError()
	if errUnwrapped.StatusCode().IsInternal() {
		errUnwrapped = err.ErrInternal.WithStatusCode(errUnwrapped.StatusCode()).
			WithMessage("Internal server error").
			Err()
	}
	return errUnwrapped
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
	}

	AbortWithStatus(ctx, errorToWrite(errFromContext))
}
