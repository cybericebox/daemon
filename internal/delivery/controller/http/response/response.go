package response

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"net/http"
)

func AbortWithBadRequest(ctx *gin.Context, err error) {
	if err == nil {
		err = fmt.Errorf("bad request")
	}
	ctx.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"message": err.Error()})
}

func LogAndAbortWithInternalServerError(ctx *gin.Context, err error) {
	log.Error().Err(err).Str("URL:", ctx.Request.URL.Path).Msg("Internal server error")
	ctx.AbortWithStatus(http.StatusInternalServerError)
}

func AbortWithUnauthorized(ctx *gin.Context, err error) {
	if err == nil {
		err = fmt.Errorf("unauthorized")
	}
	ctx.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"message": err.Error()})
}

func AbortWithForbidden(ctx *gin.Context, err error) {
	if err == nil {
		err = fmt.Errorf("forbidden")
	}
	ctx.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": err.Error()})
}

func AbortWithConflict(ctx *gin.Context, err error) {
	if err == nil {
		err = fmt.Errorf("conflict")
	}
	ctx.AbortWithStatusJSON(http.StatusConflict, gin.H{"message": err.Error()})
}

func AbortWithOK(ctx *gin.Context) {
	ctx.AbortWithStatusJSON(http.StatusOK, gin.H{"message": "ok"})
}

func AbortWithNotFound(ctx *gin.Context) {
	ctx.AbortWithStatus(http.StatusNotFound)
}

func AbortWithContent(ctx *gin.Context, content interface{}) {
	ctx.AbortWithStatusJSON(http.StatusOK, content)
}

func TemporalRedirect(ctx *gin.Context, url string) {
	ctx.Redirect(http.StatusTemporaryRedirect, url)
}
