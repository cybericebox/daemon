package auth

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

type ISignUpUseCase interface {
	SignUp(ctx context.Context, email string) error
	SignUpContinue(ctx context.Context, code string, newUser userModel.User) (*authModel.Tokens, error)
}

func (h *Handler) initSignupAPIHandler(router *gin.RouterGroup) {
	signup := router.Group("sign-up")
	{
		signup.POST("", protection.RequireRecaptcha("signUp"), h.signUp)
		signup.POST("/:code", h.signUpContinue) // two-step sign up with email confirmation
	}
}

func (h *Handler) signUp(ctx *gin.Context) {
	var inp userModel.User

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.SignUp(ctx, inp.Email); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) signUpContinue(ctx *gin.Context) {
	code := ctx.Param("code")

	var inp userModel.User

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	tokens, err := h.useCase.SignUpContinue(ctx, code, inp)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	protection.SetAuthenticated(ctx, tokens)
}
