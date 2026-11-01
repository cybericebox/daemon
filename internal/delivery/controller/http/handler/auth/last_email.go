package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

const (
	// lastEmailCookie remembers the address of the last successful sign-in in this browser, for the sign-in page to
	// prefill. It has no Domain (the host that set it only), outlives the session and sign-out, and is cleared by the
	// «not you» action and by deleting the account.
	lastEmailCookie = "cib_last_email"
	// lastEmailMaxAge is one year.
	lastEmailMaxAge = 365 * 24 * 60 * 60
)

type lastEmailResponse struct {
	// Email is the remembered address, "" when the browser has none.
	Email string `json:"Email"`
}

// startSession signs the browser in with the session cookie and remembers the account's address as its last
// sign-in address (best effort: a failure to read it only leaves the hint out).
func (h *Handler) startSession(ctx *gin.Context, sessionCookie string) {
	h.prot.Authenticate(ctx, sessionCookie)
	if sessionCookie == "" {
		return
	}
	email, err := h.useCase.SessionEmail(ctx.Request.Context(), sessionCookie)
	if err != nil {
		log.Debug().Err(err).Msg("The last sign-in address was not remembered")
		return
	}
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(lastEmailCookie, email, lastEmailMaxAge, "/", "", true, true)
}

func clearLastEmail(ctx *gin.Context) {
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(lastEmailCookie, "", -1, "/", "", true, true)
}

func lastEmail(ctx *gin.Context) string {
	value, _ := ctx.Cookie(lastEmailCookie)
	return value
}

// getLastEmail godoc
// @Summary  The address of the last sign-in in this browser (cookie), for prefilling the sign-in page
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response{data=lastEmailResponse}
// @Router   /auth/last-email [get]
func (h *Handler) getLastEmail(ctx *gin.Context) {
	response.AbortWithData(ctx, lastEmailResponse{Email: lastEmail(ctx)})
}

// forgetLastEmail godoc
// @Summary  Forget the remembered sign-in address («not you»)
// @Tags     auth
// @Success  200  {object}  response.Response
// @Router   /auth/last-email [delete]
func (h *Handler) forgetLastEmail(ctx *gin.Context) {
	clearLastEmail(ctx)
	response.AbortWithSuccess(ctx)
}
