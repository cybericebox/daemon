package auth

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type requestPasswordResetRequest struct {
	Email          string `json:"Email"`
	RecaptchaToken string `json:"RecaptchaToken"`
}

type resetPasswordRequest struct {
	Code     string `json:"Code"`
	Password string `json:"Password"`
}

type changePasswordRequest struct {
	OldPassword string `json:"OldPassword"`
	NewPassword string `json:"NewPassword"`
}

// passwordPolicyResponse is the active password complexity policy.
// SpecialCharacters lists exactly the characters counted towards MinSpecialCharacters.
type passwordPolicyResponse struct {
	MinLength            int    `json:"MinLength"`
	MaxLength            int    `json:"MaxLength"`
	MinCapitalLetters    int    `json:"MinCapitalLetters"`
	MinSmallLetters      int    `json:"MinSmallLetters"`
	MinDigits            int    `json:"MinDigits"`
	MinSpecialCharacters int    `json:"MinSpecialCharacters"`
	SpecialCharacters    string `json:"SpecialCharacters"`
}

// passwordPolicy godoc
// @Summary  Get the active password complexity policy
// @Description  Public. Thresholds are static (server config); clients use them to validate passwords before submitting.
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response{data=passwordPolicyResponse}
// @Router   /auth/password/policy [get]
func (h *Handler) passwordPolicy(ctx *gin.Context) {
	p := h.useCase.PasswordPolicy()
	response.AbortWithData(ctx, passwordPolicyResponse{
		MinLength:            p.MinLength,
		MaxLength:            p.MaxLength,
		MinCapitalLetters:    p.MinCapitalLetters,
		MinSmallLetters:      p.MinSmallLetters,
		MinDigits:            p.MinDigits,
		MinSpecialCharacters: p.MinSpecialCharacters,
		SpecialCharacters:    p.SpecialCharacters,
	})
}

// requestPasswordReset godoc
// @Summary  Request a password reset link
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      requestPasswordResetRequest  true  "email"
// @Success  200
// @Router   /auth/password/reset-request [post]
func (h *Handler) requestPasswordReset(ctx *gin.Context) {
	var req requestPasswordResetRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.ForgotPassword(ctx.Request.Context(), req.Email); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// resetPassword godoc
// @Summary  Reset password with a reset code
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      resetPasswordRequest  true  "code + new password"
// @Success  200
// @Failure  400  {object}  response.Response
// @Router   /auth/password/reset [post]
func (h *Handler) resetPassword(ctx *gin.Context) {
	var req resetPasswordRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.ResetPassword(ctx.Request.Context(), req.Code, req.Password); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// changePassword godoc
// @Summary  Change the authenticated user's password
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      changePasswordRequest  true  "old + new password"
// @Success  200
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/password/change [post]
func (h *Handler) changePassword(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	var req changePasswordRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.SetAccountPassword(
		ctx.Request.Context(),
		userID,
		req.OldPassword,
		req.NewPassword,
	); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
