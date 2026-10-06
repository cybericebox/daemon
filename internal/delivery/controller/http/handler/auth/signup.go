package auth

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

type signUpRequest struct {
	Email          string `json:"Email"`
	RecaptchaToken string `json:"RecaptchaToken"`
	// Redirect is the optional post-registration landing URL; a trusted one is
	// carried on the emailed setup link as return_to.
	Redirect string `json:"Redirect"`
}

type completeRegistrationRequest struct {
	Token      string `json:"Token"`
	FirstName  string `json:"FirstName"`
	LastName   string `json:"LastName"`
	Password   string `json:"Password"`
	TosVersion int32  `json:"TosVersion"`
	Redirect   string `json:"Redirect"`
}

type setupContextResponse struct {
	Email       string `json:"Email"`
	FirstName   string `json:"FirstName"`
	LastName    string `json:"LastName"`
	HasProvider bool   `json:"HasProvider"`
}

// signUp godoc
// @Summary  Begin email registration
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      signUpRequest  true  "email"
// @Success  200
// @Failure  400  {object}  response.Response
// @Router   /auth/sign-up [post]
func (h *Handler) signUp(ctx *gin.Context) {
	var req signUpRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.BeginEmailRegistration(ctx, req.Email, req.Redirect); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// getSetup godoc
// @Summary  Resolve a setup token to the registration-completion screen
// @Tags     auth
// @Produce  json
// @Param    token  query  string  true  "setup token"
// @Success  200  {object}  response.Response{data=setupContextResponse}
// @Failure  400  {object}  response.Response
// @Router   /auth/setup [get]
func (h *Handler) getSetup(ctx *gin.Context) {
	sc, err := h.useCase.GetSetupContext(ctx, ctx.Query("token"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, setupContextResponse{
		Email:       sc.Email,
		FirstName:   sc.FirstName,
		LastName:    sc.LastName,
		HasProvider: sc.HasProvider,
	})
}

// completeSetup godoc
// @Summary  Complete registration (set name, password, ToS) and sign in
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      completeRegistrationRequest  true  "completion"
// @Success  200  {object}  response.Response{data=object}
// @Failure  400  {object}  response.Response
// @Router   /auth/setup [post]
func (h *Handler) completeSetup(ctx *gin.Context) {
	var req completeRegistrationRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	meta := authModel.SessionMetadata{UserAgent: ctx.Request.UserAgent(), IP: ctx.ClientIP()}
	cookie, redirect, err := h.useCase.CompleteRegistration(
		ctx,
		req.Token,
		req.FirstName,
		req.LastName,
		req.Password,
		req.TosVersion,
		req.Redirect,
		meta,
	)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	h.prot.Authenticate(ctx, cookie)
	response.AbortWithData(ctx, gin.H{"RedirectURL": redirect})
}
