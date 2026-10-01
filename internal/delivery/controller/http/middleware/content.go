package middleware

import "github.com/gin-gonic/gin"

func ContentMiddleware(ctx *gin.Context) {
	ctx.Header("Content-Type", "application/json")
}
