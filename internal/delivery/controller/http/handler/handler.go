package handler

import (
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/docs"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/user"
	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

type (
	Handler struct {
		useCase IUseCase
	}

	IUseCase interface {
		auth.IUseCase
		event.IUseCase
		exercise.IUseCase
		user.IUseCase
	}
)

// @title           Cyber ICE Box Platform API
// @version         1.0
// @description     This is the API for the Cyber ICE Box Platform

// @BasePath  /api

func NewAPIHandler(useCase IUseCase) *Handler {
	return &Handler{useCase: useCase}
}

func (h *Handler) Init(router *gin.Engine) {
	baseAPI := router.Group("api", corsMiddleware)
	{
		// swagger docs
		docs.SwaggerInfo.Host = fmt.Sprintf("%s://%s", config.SchemeHTTPS, config.PlatformDomain)
		baseAPI.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

		auth.NewAuthAPIHandler(h.useCase).Init(baseAPI)
		event.NewEventAPIHandler(h.useCase).Init(baseAPI)
		exercise.NewExerciseAPIHandler(h.useCase).Init(baseAPI) // all routes are protected
		user.NewUserAPIHandler(h.useCase).Init(baseAPI)         // all routes are protected
	}
}

func corsMiddleware(ctx *gin.Context) {
	ctx.Header("Content-Type", "application/json")
}
