package response

import (
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gin-gonic/gin"
	"net/http"
)

type (
	Status struct {
		Code    int
		Message string
		Details map[string]interface{}
	}

	Response struct {
		Status Status
		Data   interface{}
	}
)

func AbortWithData(ctx *gin.Context, data interface{}, statusCode ...err.Error) {
	code := err.Success.Err()

	if len(statusCode) > 0 {
		code = statusCode[0]
	}
	ctx.JSON(http.StatusOK, Response{
		Status: Status{
			Code:    code.Code().Code(),
			Message: code.Code().Message(),
		},
		Data: data,
	})
}

func AbortWithStatus(ctx *gin.Context, code err.Error) {
	ctx.AbortWithStatusJSON(code.Code().HTTPCode(), Response{
		Status: Status{
			Code:    code.Code().Code(),
			Message: code.Code().Message(),
		},
	})
}

func AbortWithBadRequest(ctx *gin.Context, errs ...error) {
	message := "Invalid input data"
	if len(errs) > 0 && errs[0] != nil {
		message = errs[0].Error()
	}

	AbortWithStatus(ctx, err.ErrInvalidData.WithMessage(message).Err())
}

func AbortWithUnauthenticated(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusUnauthorized)
}

func AbortWithForbidden(ctx *gin.Context) {
	AbortWithStatus(ctx, err.ErrForbidden.Err())
}

func AbortWithNotFound(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusNotFound)
}

func AbortWithSuccess(ctx *gin.Context) {
	AbortWithStatus(ctx, err.Success.Err())
}

func AbortWithTooManyRequests(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusTooManyRequests)
}

func AbortWithError(ctx *gin.Context, err error) {
	// set errorWrapper to context
	ctx.Set(tools.ErrorCtxKey, err)
	ctx.Abort()
}

func TemporaryRedirect(ctx *gin.Context, url string) {
	ctx.Redirect(http.StatusTemporaryRedirect, url)
}
