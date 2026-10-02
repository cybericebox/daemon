package auth

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type accountResponse struct {
	FirstName      string    `json:"FirstName"`
	LastName       string    `json:"LastName"`
	Email          string    `json:"Email"`
	Picture        string    `json:"Picture"`
	EmailConfirmed bool      `json:"EmailConfirmed"`
	Role           rbac.Role `json:"Role"`
	Providers      []string  `json:"Providers"`
	HasPassword    bool      `json:"HasPassword"`
	CreatedAt      time.Time `json:"CreatedAt"`
}

type updateProfileRequest struct {
	FirstName string `json:"FirstName"`
	LastName  string `json:"LastName"`
}

type requestEmailChangeRequest struct {
	Email string `json:"Email"`
	// CurrentPassword re-authenticates the change (the stolen-session guard).
	CurrentPassword string `json:"CurrentPassword"`
}

type confirmEmailChangeRequest struct {
	Code string `json:"Code"`
}

type meResponse struct {
	ID          uuid.UUID         `json:"ID"`
	FirstName   string            `json:"FirstName"`
	LastName    string            `json:"LastName"`
	Email       string            `json:"Email"`
	Picture     string            `json:"Picture"`
	Role        rbac.Role         `json:"Role"`
	Permissions []rbac.Permission `json:"Permissions"`
}

// getAccount godoc
// @Summary  Get the authenticated user's account details
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response{data=accountResponse}
// @Failure  401  {object}  response.Response
// @Router   /auth/account [get]
func (h *Handler) getAccount(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	acc, err := h.useCase.GetAccount(ctx.Request.Context(), userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, accountResponse{
		FirstName: acc.FirstName, LastName: acc.LastName, Email: acc.Email, Picture: acc.Picture,
		EmailConfirmed: acc.EmailConfirmed, Role: acc.Role, Providers: acc.Providers,
		HasPassword: acc.HasPassword, CreatedAt: acc.CreatedAt,
	})
}

// getSelfProfile godoc
// @Summary  Get the authenticated user's public profile
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/me [get]
func (h *Handler) getSelfProfile(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	p, err := h.useCase.GetSelfProfile(ctx.Request.Context(), userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, meResponse{
		ID:          p.ID,
		FirstName:   p.FirstName,
		LastName:    p.LastName,
		Email:       p.Email,
		Picture:     p.Picture,
		Role:        p.Role,
		Permissions: p.Role.Permissions(),
	})
}

// updateProfile godoc
// @Summary  Update the authenticated user's profile (first name, last name)
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      updateProfileRequest  true  "profile fields"
// @Success  200
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/account/profile [patch]
func (h *Handler) updateProfile(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	var req updateProfileRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.UpdateAccountProfile(
		ctx.Request.Context(),
		userID,
		req.FirstName,
		req.LastName,
	); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// requestEmailChange godoc
// @Summary  Request an email address change (sends confirmation to the new address)
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      requestEmailChangeRequest  true  "new email address"
// @Success  200
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/account/email [post]
func (h *Handler) requestEmailChange(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	var req requestEmailChangeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.RequestEmailChange(ctx.Request.Context(), userID, req.Email, req.CurrentPassword); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// confirmEmailChange godoc
// @Summary  Confirm an email address change using the code sent to the new address
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      confirmEmailChangeRequest  true  "confirmation code"
// @Success  200
// @Failure  400  {object}  response.Response
// @Router   /auth/account/email/confirm [post]
func (h *Handler) confirmEmailChange(ctx *gin.Context) {
	var req confirmEmailChangeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.ConfirmEmailChange(ctx.Request.Context(), req.Code); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// deleteAccount godoc
// @Summary  Delete (soft-delete) the authenticated user's account
// @Tags     auth
// @Produce  json
// @Success  200
// @Failure  401  {object}  response.Response
// @Router   /auth/account [delete]
func (h *Handler) deleteAccount(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if err := h.useCase.DeleteAccount(ctx.Request.Context(), userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	h.prot.DeAuthenticate(ctx) // clears cookies + returns success
}
