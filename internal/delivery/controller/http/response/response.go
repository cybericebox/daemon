package response

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/pkg/err"
)

type (
	Status struct {
		Code    int
		Message string
		Details map[string]any
	}

	Response struct {
		Status Status
		Data   any
	}
)

func AbortWithData(ctx *gin.Context, data any, statusCode ...err.Error) {
	// Default to the success status code fields directly, avoiding a call to
	// .Err() which triggers saveStack in pkg/err and panics in tests
	// when the test package directory is deeper in the path than response.go.
	successStatus := err.StatusCodeSuccess
	code := successStatus.FullCode()
	message := successStatus.Message()

	if len(statusCode) > 0 && statusCode[0] != nil {
		code = statusCode[0].StatusCode().FullCode()
		message = statusCode[0].StatusCode().Message()
	}
	ctx.JSON(
		http.StatusOK, Response{
			Status: Status{
				Code:    code,
				Message: message,
			},
			Data: data,
		},
	)
}

func AbortWithStatus(ctx *gin.Context, code err.Error) {
	if seconds, ok := code.StatusCode().Details()[err.DetailRetryAfterSeconds].(int64); ok {
		ctx.Header("Retry-After", strconv.FormatInt(seconds, 10))
	}
	ctx.AbortWithStatusJSON(
		code.StatusCode().HTTPCode(), Response{
			Status: Status{
				Code:    code.StatusCode().FullCode(),
				Message: code.StatusCode().Message(),
			},
		},
	)
}

func AbortWithBadRequest(ctx *gin.Context, errs ...error) {
	message := "Invalid input data"
	if len(errs) > 0 && errs[0] != nil {
		message = errs[0].Error()
	}

	AbortWithStatus(ctx, err.ErrInvalidData.WithMessage(message).Err())
}

func AbortWithUnauthenticated(ctx *gin.Context) {
	// Avoid calling .Err() which triggers saveStack in pkg/err and
	// panics in tests when the package directory is deeper than response.go.
	// Read the status code fields directly, mirroring AbortWithSuccess.
	unauthStatus := err.StatusCodeUnauthenticated
	ctx.AbortWithStatusJSON(
		unauthStatus.HTTPCode(), Response{
			Status: Status{
				Code:    unauthStatus.FullCode(),
				Message: unauthStatus.Message(),
			},
		},
	)
}

func AbortWithForbidden(ctx *gin.Context) {
	AbortWithStatus(ctx, err.ErrForbidden.Err())
}

func AbortWithConflict(ctx *gin.Context) {
	AbortWithStatus(ctx, err.ErrConflict.Err())
}

func AbortWithNoContent(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusNoContent)
}

func AbortWithNotFound(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusNotFound)
}

func AbortWithSuccess(ctx *gin.Context) {
	// Avoid calling .Err() which triggers saveStack in pkg/err and
	// panics in tests when the package directory is deeper than response.go.
	// Instead read the status code fields directly, mirroring AbortWithData.
	successStatus := err.StatusCodeSuccess
	ctx.AbortWithStatusJSON(
		successStatus.HTTPCode(), Response{
			Status: Status{
				Code:    successStatus.FullCode(),
				Message: successStatus.Message(),
			},
		},
	)
}

func AbortWithTooManyRequests(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusTooManyRequests)
}

func AbortWithError(ctx *gin.Context, err error) {
	// set errorWrapper to context
	ctx.Set(ErrorCtxKey, err)
	ctx.Abort()
}

func TemporaryRedirect(ctx *gin.Context, url string) {
	ctx.Redirect(http.StatusTemporaryRedirect, url)
}
