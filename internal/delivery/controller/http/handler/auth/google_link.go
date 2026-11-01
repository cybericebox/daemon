package auth

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// googleLinkTicketMaxAge is the life of the ticket cookie (the ticket itself lives as long on the server).
const googleLinkTicketMaxAge = 600

type googleLinkPendingResponse struct {
	// Email is the address of the account, masked (a***@example.org).
	Email string `json:"Email"`
	// HasPassword: confirm with the password or the mailed code; false: only the code.
	HasPassword bool `json:"HasPassword"`
	// GoogleEmail and GoogleName are the Google account that will be linked; show them on the confirmation screen.
	GoogleEmail string `json:"GoogleEmail"`
	GoogleName  string `json:"GoogleName"`
	// CodeSentAt is when the last code was mailed, null if none was.
	CodeSentAt *time.Time `json:"CodeSentAt"`
	ExpiresAt  time.Time  `json:"ExpiresAt"`
}

type googleLinkCodeResponse struct {
	// ResendAfterSeconds is how long to wait before asking for the next code.
	ResendAfterSeconds int64 `json:"ResendAfterSeconds"`
}

type googleLinkConfirmRequest struct {
	// Exactly one of Password and Code.
	Password string `json:"Password"`
	Code     string `json:"Code"`
}

// googleLinkTicket reads the ticket cookie; an absent one is the plain «not found» of an unknown ticket.
func googleLinkTicket(ctx *gin.Context) string {
	value, _ := ctx.Cookie(googleLinkTicketCookie)
	return value
}

func clearGoogleLinkTicket(ctx *gin.Context) {
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(googleLinkTicketCookie, "", -1, oauthCookiePath, "", true, true)
}

// googleLinkPending godoc
// @Summary  The Google link waiting for the owner's confirmation (ticket cookie)
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response{data=googleLinkPendingResponse}
// @Failure  404  {object}  response.Response
// @Router   /auth/google/pending [get]
func (h *Handler) googleLinkPending(ctx *gin.Context) {
	view, err := h.useCase.GoogleLinkPendingOf(ctx.Request.Context(), googleLinkTicket(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, googleLinkPendingResponse{Email: view.Email, HasPassword: view.HasPassword, GoogleEmail: view.GoogleEmail, GoogleName: view.GoogleName, CodeSentAt: view.CodeSentAt, ExpiresAt: view.ExpiresAt})
}

// googleLinkSendCode godoc
// @Summary  Mail the one-time code that confirms the pending Google link (60 s apart, 5 per hour)
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response{data=googleLinkCodeResponse}
// @Failure  404  {object}  response.Response
// @Failure  429  {object}  response.Response
// @Router   /auth/google/pending/code [post]
func (h *Handler) googleLinkSendCode(ctx *gin.Context) {
	sent, err := h.useCase.SendGoogleLinkCode(ctx.Request.Context(), googleLinkTicket(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, googleLinkCodeResponse{ResendAfterSeconds: int64(sent.ResendAfter.Seconds())})
}

// googleLinkConfirm godoc
// @Summary  Confirm the pending Google link with the password or the mailed code, link it and sign in
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body  googleLinkConfirmRequest  true  "exactly one of Password and Code"
// @Success  200  {object}  response.Response{data=object}
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Failure  429  {object}  response.Response
// @Router   /auth/google/pending/confirm [post]
func (h *Handler) googleLinkConfirm(ctx *gin.Context) {
	var req googleLinkConfirmRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	cookie, redirect, err := h.useCase.ConfirmGoogleLink(ctx.Request.Context(), googleLinkTicket(ctx), req.Password, req.Code, sessionMetadata(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	clearGoogleLinkTicket(ctx)
	h.prot.Authenticate(ctx, cookie)
	response.AbortWithData(ctx, gin.H{"RedirectURL": redirect})
}

// googleLinkCancel godoc
// @Summary  Give up the pending Google link
// @Tags     auth
// @Success  200  {object}  response.Response
// @Router   /auth/google/pending [delete]
func (h *Handler) googleLinkCancel(ctx *gin.Context) {
	if err := h.useCase.CancelGoogleLink(ctx.Request.Context(), googleLinkTicket(ctx)); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	clearGoogleLinkTicket(ctx)
	response.AbortWithSuccess(ctx)
}
