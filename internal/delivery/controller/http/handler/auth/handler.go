package auth

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gin-gonic/gin"
)

type (
	Handler struct {
		useCase IUseCase
	}

	IUseCase interface {
		SignIn(ctx context.Context, email, password string) (*model.Tokens, error)

		ISignUpUseCase
		IPasswordUseCase
		IEmailUseCase
		IGoogleUseCase
		ISelfUseCase
	}
)

func NewAuthAPIHandler(useCase IUseCase) *Handler {
	return &Handler{useCase: useCase}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	authAPI := router.Group("auth")
	{
		authAPI.POST("sign-in", protection.RequireRecaptcha("signIn"), h.signIn)
		authAPI.POST("sign-out", h.signOut)

		h.initSignupAPIHandler(authAPI)
		h.initPasswordAPIHandler(authAPI)
		h.initEmailAPIHandler(authAPI)
		h.initSelfAPIHandler(authAPI)

		//with oauth
		oauth := authAPI.Group("")
		{
			h.initOAuthGoogleAPIHandler(oauth)
		}
	}
}

type signInRequest struct {
	Email    string `json:"email" binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,min=1,max=64"`
}

func (h *Handler) signIn(ctx *gin.Context) {
	var inp signInRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	tokens, err := h.useCase.SignIn(ctx, inp.Email, inp.Password)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	protection.SetAuthenticated(ctx, tokens)
}

func (h *Handler) signOut(ctx *gin.Context) {
	protection.DeAuthenticateAndAbortWithOk(ctx)
}
