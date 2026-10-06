package auth

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

type signInRequest struct {
	Email          string `json:"Email"`
	Password       string `json:"Password"`
	Redirect       string `json:"Redirect"`
	RecaptchaToken string `json:"RecaptchaToken"`
}

type sessionResponse struct {
	ID        uuid.UUID `json:"ID"`
	UserAgent string    `json:"UserAgent"`
	IP        string    `json:"IP"`
	LastSeen  time.Time `json:"LastSeen"`
	CreatedAt time.Time `json:"CreatedAt"`
	IsCurrent bool      `json:"IsCurrent"`
}

func toSessionResponse(s authUseCase.SessionInfo) sessionResponse {
	return sessionResponse{
		ID:        s.ID,
		UserAgent: s.UserAgent,
		IP:        s.IP,
		LastSeen:  s.LastSeen,
		CreatedAt: s.CreatedAt,
		IsCurrent: s.IsCurrent,
	}
}

// signIn godoc
// @Summary  Sign in with email and password
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      signInRequest  true  "credentials"
// @Success  200  {object}  response.Response{data=object}
// @Failure  400  {object}  response.Response
// @Router   /auth/sign-in [post]
func (h *Handler) signIn(ctx *gin.Context) {
	var req signInRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	meta := authModel.SessionMetadata{
		UserAgent: ctx.Request.UserAgent(),
		IP:        ctx.ClientIP(),
	}
	cookie, redirect, err := h.useCase.SignIn(
		ctx.Request.Context(),
		req.Email,
		req.Password,
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

// listSessions godoc
// @Summary  List the current user's active sessions
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response{data=[]sessionResponse}
// @Failure  401  {object}  response.Response
// @Router   /auth/sessions [get]
func (h *Handler) listSessions(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	currentSessionID := claims.SessionID
	items, err := h.useCase.ListSessions(ctx.Request.Context(), userID, currentSessionID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]sessionResponse, 0, len(items))
	for _, it := range items {
		out = append(out, toSessionResponse(it))
	}
	response.AbortWithData(ctx, out)
}

// revokeSession godoc
// @Summary  Revoke one of the current user's sessions
// @Tags     auth
// @Produce  json
// @Param    id   path      string  true  "session ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/sessions/{id} [delete]
func (h *Handler) revokeSession(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	sessionID, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.RevokeSession(ctx.Request.Context(), userID, sessionID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// revokeOtherSessions godoc
// @Summary  Revoke all of the current user's sessions except the current one
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/sessions [delete]
func (h *Handler) revokeOtherSessions(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	currentSessionID := claims.SessionID
	if err := h.useCase.RevokeOtherSessions(
		ctx.Request.Context(),
		userID,
		currentSessionID,
	); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
