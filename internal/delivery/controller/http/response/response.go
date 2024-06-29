package response

import (
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"net/http"
)

func AbortWithStatus(ctx *gin.Context, statusCode int, message string) {
	ctx.AbortWithStatusJSON(statusCode, gin.H{"message": message})
}

func AbortWithBadRequest(ctx *gin.Context, err ...error) {
	if len(err) == 0 || err[0] == nil {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": "Bad request"})
	} else {
		ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err[0].Error()})
	}
}

func LogAndAbortWithInternalServerError(ctx *gin.Context, err error) {
	log.Error().Err(err).Str("URL:", ctx.Request.URL.Path).Msg("Internal server error")
	ctx.AbortWithStatus(http.StatusInternalServerError)
}

func AbortWithUnauthorized(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusUnauthorized)
}

func AbortWithNotFound(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusNotFound)
}

func AbortWithOK(ctx *gin.Context, message string) {
	ctx.AbortWithStatusJSON(http.StatusOK, gin.H{"message": message})
}

func AbortWithContent(ctx *gin.Context, content interface{}) {
	ctx.AbortWithStatusJSON(http.StatusOK, content)
}

func Redirect(ctx *gin.Context, status int, url string) {
	ctx.Redirect(status, url)
}

func AbortWithError(ctx *gin.Context, err error) {
	// set errorWrapper to context
	ctx.Set(tools.ErrorCtxKey, err)
	ctx.Abort()
}
