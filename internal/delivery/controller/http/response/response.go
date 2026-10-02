package response

import (
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/pkg/err"
)

// StreamRetryAfter is the wait a refused live-stream connection is told: a slot frees when the reader's
// other stream closes, which has no known time.
const StreamRetryAfter = 5 * time.Second

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

// AbortWithUnsupportedMediaType refuses a body that is not in a type the API reads.
func AbortWithUnsupportedMediaType(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusUnsupportedMediaType)
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

// AbortWithTooManyRequests is the one 429 every limiter answers with: the ErrAuthTooManyRequests code in the
// normal error envelope, and Retry-After with the wait rounded up to whole seconds (at least one).
func AbortWithTooManyRequests(ctx *gin.Context, wait time.Duration) {
	seconds := max(1, int64(math.Ceil(wait.Seconds())))
	AbortWithStatus(ctx, authModel.ErrAuthTooManyRequests.WithDetail(err.DetailRetryAfterSeconds, seconds).Err())
}

func AbortWithError(ctx *gin.Context, err error) {
	// set errorWrapper to context
	ctx.Set(ErrorCtxKey, err)
	ctx.Abort()
}

func TemporaryRedirect(ctx *gin.Context, url string) {
	ctx.Redirect(http.StatusTemporaryRedirect, url)
}
