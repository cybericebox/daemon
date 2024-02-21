package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/user"
	"github.com/gin-gonic/gin"
)

type signUpService interface {
	SignUp(ctx context.Context, email string) error
	SignUpContinue(ctx context.Context, code string, newUser *user.User) (*auth.Tokens, error)
}

type signUpRequest struct {
	Email string `json:"email" binding:"required,email,max=255"`
}

func (h *Handler) signUp(ctx *gin.Context) {
	var inp signUpRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.service.SignUp(ctx, inp.Email); err != nil {
		// use abstraction to log error and abort with status
		response.LogAndAbortWithInternalServerError(ctx, err)
		return
	}
	response.AbortWithOK(ctx)
}

type signUpContinueRequest struct {
	Email    string `json:"email" binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,min=8,max=64"`
	Name     string `json:"name" binding:"required,min=2,max=255"`
}

func (h *Handler) signUpContinue(ctx *gin.Context) {
	token := ctx.Param("token")

	var inp signUpContinueRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	tokens, err := h.service.SignUpContinue(ctx, token, &user.User{
		Email:    inp.Email,
		Name:     inp.Name,
		Password: inp.Password,
	})
	if err != nil {
		if errors.Is(err, auth.ErrInvalidTemporalCode) {
			response.AbortWithUnauthorized(ctx, err)
			return
		}

		if errors.Is(err, auth.ErrInvalidPasswordComplexity) {
			response.AbortWithBadRequest(ctx, err)
			return
		}

		response.LogAndAbortWithInternalServerError(ctx, err)
		return
	}

	// get tokens and set them to the cookies
	protection.SetTokens(ctx, tokens)

	response.AbortWithOK(ctx)
}
