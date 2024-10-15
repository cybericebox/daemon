package handler

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/user"
	"github.com/gin-gonic/gin"
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

func NewAPIHandler(useCase IUseCase) *Handler {
	return &Handler{useCase: useCase}
}

func (h *Handler) Init(router *gin.Engine) {
	baseAPI := router.Group("api", corsMiddleware)
	{
		auth.NewAuthAPIHandler(h.useCase).Init(baseAPI)
		event.NewEventAPIHandler(h.useCase).Init(baseAPI)
		exercise.NewExerciseAPIHandler(h.useCase).Init(baseAPI) // all routes are protected
		user.NewUserAPIHandler(h.useCase).Init(baseAPI)         // all routes are protected
	}
}

func corsMiddleware(ctx *gin.Context) {
	ctx.Header("Content-Type", "application/json")
}
