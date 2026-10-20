package response

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/pkg/err"

	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/pkg/overload"
)

const ErrorCtxKey = "error"

// OverloadCtxKey marks a request refused because the server is saturated (in-flight limiter, busy database pool).
// Such a refusal is protection working, not a fault: the error journal skips it.
const OverloadCtxKey = "overload"

// IsOverload reports whether the request was refused for overload.
func IsOverload(ctx *gin.Context) bool {
	_, ok := ctx.Get(OverloadCtxKey)
	return ok
}

// IsDBBusy reports whether the request ends with the DB-busy 503: its stored error comes from a database
// call that found no free connection in time. It works before WithErrorHandler has written the response.
func IsDBBusy(ctx *gin.Context) bool {
	stored := getErrorFromContext(ctx)
	return stored != nil && errors.Is(stored, overload.ErrDBBusy)
}

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

	// Pool saturation surfaces from any depth of use-case wrapping: answer it as a retryable 503, not an internal error.
	if errors.Is(errFromContext, overload.ErrDBBusy) {
		ctx.Set(OverloadCtxKey, true)
		AbortWithStatus(
			ctx,
			authModel.ErrDatabaseBusy.WithDetail(err.DetailRetryAfterSeconds, int64(1)).Err(),
		)
		return
	}

	// The client left: nobody reads the answer, so 499 instead of a fault. The journal skips it (below 500).
	if ctx.Request.Context().Err() == context.Canceled && OnlyCancellation(errFromContext) {
		AbortWithStatus(ctx, authModel.ErrRequestCancelled.Err())
		return
	}

	// The database is down, restarting or dropped the connection: retryable 503. Unlike the busy pool this is
	// a fault, so no OverloadCtxKey: the error journal records it and the notifications go out.
	if isDBUnavailable(errFromContext) {
		log.Error().Err(errFromContext).Str("url", ctx.Request.URL.Path).Msg("Database unavailable")
		AbortWithStatus(
			ctx,
			authModel.ErrDatabaseUnavailable.WithDetail(err.DetailRetryAfterSeconds, int64(1)).Err(),
		)
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

// isDBUnavailable reports whether the error chain says the database cannot be used right now: it cannot be
// reached, it shut the connection down (57P01 admin shutdown, 57P02 crash shutdown, 57P03 cannot connect now,
// class 08 connection exceptions) or the pool is closed. Any depth of use-case wrapping is looked through.
func isDBUnavailable(e error) bool {
	var pgErr *pgconn.PgError
	if errors.As(e, &pgErr) {
		return strings.HasPrefix(pgErr.Code, "08") || pgErr.Code == "57P01" || pgErr.Code == "57P02" || pgErr.Code == "57P03"
	}
	var connectErr *pgconn.ConnectError
	if errors.As(e, &connectErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(e, &opErr) && opErr.Op == "dial" {
		return true
	}
	// puddle's ErrClosedPool and pgconn's closed connection are not exported by a package we import.
	text := e.Error()
	return strings.Contains(text, "closed pool") || strings.Contains(text, "conn closed")
}

// OnlyCancellation reports whether the error is a context cancellation and nothing else. It inspects every branch of a joined error: cancellation must not hide a simultaneous server fault.
func OnlyCancellation(err error) bool {
	if err == context.Canceled {
		return true
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		children := wrapped.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, child := range children {
			if !OnlyCancellation(child) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return OnlyCancellation(wrapped.Unwrap())
	default:
		return false
	}
}

// PublicError is what a client sees of an error: its full code and message, an internal one masked as in a
// response. Bulk endpoints report it per item.
func PublicError(e error) (int, string) {
	var parsed err.Error
	if !errors.As(e, &parsed) {
		parsed = model.ErrPlatform.WithError(e).WithMessage(e.Error()).Err()
	}
	shown := errorToWrite(parsed)
	return shown.StatusCode().FullCode(), shown.StatusCode().Message()
}
