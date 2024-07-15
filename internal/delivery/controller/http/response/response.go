package response

import (
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"net/http"
)

func AbortWithCode(ctx *gin.Context, code appError.Code) {
	ctx.AbortWithStatusJSON(code.GetHTTPCode(), gin.H{"Code": code.GetInformCode(), "Message": code.GetMessage()})
}

func AbortWithBadRequest(ctx *gin.Context, err ...error) {
	message := "Invalid input data"
	if len(err) > 0 && err[0] != nil {
		message = err[0].Error()
	}

	AbortWithCode(ctx, appError.CodeInvalidInput.WithMessage(message))
}

func AbortWithUnauthorized(ctx *gin.Context) {
	AbortWithCode(ctx, appError.CodeUnauthorized)
}

func AbortWithForbidden(ctx *gin.Context) {
	AbortWithCode(ctx, appError.CodeForbidden)
}

func AbortWithNotFound(ctx *gin.Context) {
	AbortWithCode(ctx, appError.CodeNotFound)
}

func AbortWithContent(ctx *gin.Context, content interface{}) {
	ctx.AbortWithStatusJSON(http.StatusOK, content)
}

func AbortWithSuccess(ctx *gin.Context) {
	AbortWithCode(ctx, appError.CodeSuccess)
}

func AbortWithError(ctx *gin.Context, err error) {
	// set errorWrapper to context
	ctx.Set(tools.ErrorCtxKey, err)
	ctx.Abort()
}

func TemporaryRedirect(ctx *gin.Context, url string) {
	ctx.Redirect(http.StatusTemporaryRedirect, url)
}
