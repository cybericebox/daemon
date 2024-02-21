package handler

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/gin-gonic/gin"
)

func corsMiddleware(ctx *gin.Context) {
	//ctx.Header("Access-Control-Allow-Origin", "*")
	//ctx.Header("Access-Control-Allow-Methods", "*")
	//ctx.Header("Access-Control-Allow-Headers", "*")
	ctx.Header("Content-Type", "application/json")

	if ctx.Request.Method != "OPTIONS" {
		ctx.Next()
	} else {
		response.AbortWithOK(ctx)
	}
}
