package auth

import (
	"crypto/subtle"
	"errors"

	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

const (
	oauthIntentCookie      = "cib_oauth_intent"
	oauthSetupTokenCookie  = "cib_oauth_setup_token"
	oauthStateCookie       = "cib_oauth_state"
	oauthLinkSessionCookie = "cib_oauth_link_sid"
)

// redirectFromQuery validates the ?return_to= query param against the platform
// domain, returning "" for missing/untrusted input — GetGoogleLoginURL then
// resolves "" to the default landing page at callback time.
func (h *Handler) redirectFromQuery(ctx *gin.Context) string {
	raw := ctx.Query("return_to")
	if raw == "" || !h.useCase.IsTrustedRedirect(raw) {
		return ""
	}
	return raw
}

// googleRedirect godoc
// @Summary  Begin Google sign-in (redirects to Google OAuth)
// @Tags     auth
// @Param    return_to  query  string  false  "post-auth landing URL"
// @Success  307
// @Failure  500  {object}  response.Response
// @Router   /auth/google [get]
// googleRedirect starts Google sign-in (no intent cookie → callback treats as sign-in).
func (h *Handler) googleRedirect(ctx *gin.Context) {
	url, state, err := h.useCase.GetGoogleLoginURL(h.redirectFromQuery(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(oauthStateCookie, state, h.oauthCookieMaxAge, "/api/auth", "", true, true)
	response.TemporaryRedirect(ctx, url)
}

// googleRegisterRedirect godoc
// @Summary  Begin Google registration (sets register intent cookie, redirects to Google OAuth)
// @Tags     auth
// @Param    return_to  query  string  false  "post-registration landing URL (kept on /setup)"
// @Success  307
// @Failure  500  {object}  response.Response
// @Router   /auth/google/register [get]
// googleRegisterRedirect starts register-with-Google (sets the register intent cookie).
func (h *Handler) googleRegisterRedirect(ctx *gin.Context) {
	url, state, err := h.useCase.GetGoogleLoginURL(h.redirectFromQuery(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(oauthStateCookie, state, h.oauthCookieMaxAge, "/api/auth", "", true, true)
	ctx.SetCookie(oauthIntentCookie, "register", h.oauthCookieMaxAge, "/api/auth", "", true, true)
	response.TemporaryRedirect(ctx, url)
}

// googleSetupRedirect godoc
// @Summary  Link Google to an incomplete (setup) account
// @Tags     auth
// @Param    token      query     string  true   "setup token"
// @Param    return_to  query     string  false  "post-registration landing URL (kept on /setup)"
// @Success  307
// @Failure  400  {object}  response.Response
// @Failure  500  {object}  response.Response
// @Router   /auth/google/setup [get]
// googleSetupRedirect starts linking Google to an incomplete account during setup.
func (h *Handler) googleSetupRedirect(ctx *gin.Context) {
	setupToken := ctx.Query("token")
	if setupToken == "" {
		response.AbortWithBadRequest(ctx, nil)
		return
	}
	url, state, err := h.useCase.GetGoogleLoginURL(h.redirectFromQuery(ctx))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(oauthStateCookie, state, h.oauthCookieMaxAge, "/api/auth", "", true, true)
	ctx.SetCookie(oauthIntentCookie, "setup", h.oauthCookieMaxAge, "/api/auth", "", true, true)
	ctx.SetCookie(oauthSetupTokenCookie, setupToken, h.oauthCookieMaxAge, "/api/auth", "", true, true)
	response.TemporaryRedirect(ctx, url)
}

// googleCallback godoc
// @Summary  Google OAuth callback — dispatches by intent cookie (sign-in / register / setup)
// @Tags     auth
// @Param    code   query     string  true  "OAuth authorization code"
// @Param    state  query     string  true  "OAuth state"
// @Success  307
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Failure  500  {object}  response.Response
// @Router   /auth/google/callback [get]
// googleCallback is the single Google OAuth redirect target; it dispatches by the
// short-lived intent cookie (absent = sign-in), which it reads and clears. Every
// redirect target here is an ABSOLUTE https://id.<domain>/... URL: this handler
// runs on api.<domain>, not on the id frontend's own host, so a relative
// Location would 404 against the API.
func (h *Handler) googleCallback(ctx *gin.Context) {
	code := ctx.Query("code")
	state := ctx.Query("state")

	// CSRF / login-fixation guard: verify the state cookie double-submit binding.
	stateCookie, _ := ctx.Cookie(oauthStateCookie)
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(oauthStateCookie, "", -1, "/api/auth", "", true, true)
	if stateCookie == "" || subtle.ConstantTimeCompare([]byte(stateCookie), []byte(state)) != 1 {
		h.googleErrorRedirect(ctx, "/sign-in", "failed", "")
		return
	}

	intent, _ := ctx.Cookie(oauthIntentCookie)
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(oauthIntentCookie, "", -1, "/api/auth", "", true, true)

	meta := authModel.SessionMetadata{UserAgent: ctx.Request.UserAgent(), IP: ctx.ClientIP()}

	switch intent {
	case "register":
		res, err := h.useCase.BeginGoogleRegistration(
			ctx.Request.Context(),
			code,
			state,
			meta,
		)
		if err != nil {
			switch {
			case errors.Is(err, authModel.ErrAuthAccountBlocked.Err()):
				h.googleErrorRedirect(ctx, "/sign-in", "blocked", "")
			case errors.Is(err, authModel.ErrAuthAccountExistsSignIn.Err()):
				// Email owned by an active account without Google: sign in there
				// (Google can then be linked from the profile). Never auto-link.
				h.googleErrorRedirect(ctx, "/sign-in", "already_registered", "")
			default:
				h.googleErrorRedirect(ctx, "/sign-up", "failed", "")
			}
			return
		}
		if res.SessionCookie != "" {
			// Google already linked to a finished account: plain sign-in.
			h.prot.Authenticate(ctx, res.SessionCookie)
			response.TemporaryRedirect(ctx, res.Redirect)
			return
		}
		response.TemporaryRedirect(ctx, h.setupURL(res.SetupToken, res.ReturnTo, ""))

	case "setup":
		setupToken, _ := ctx.Cookie(oauthSetupTokenCookie)
		ctx.SetSameSite(http.SameSiteLaxMode)
		ctx.SetCookie(oauthSetupTokenCookie, "", -1, "/api/auth", "", true, true)
		if setupToken == "" {
			h.googleErrorRedirect(ctx, "/sign-in", "failed", "")
			return
		}
		returnTo, err := h.useCase.LinkGoogleToSetupFromOAuth(
			ctx.Request.Context(),
			setupToken,
			code,
			state,
		)
		if err != nil {
			// The setup token is still ours: back to setup with a link error
			// rather than a generic sign-in failure.
			response.TemporaryRedirect(ctx, h.setupURL(setupToken, returnTo, "link_failed"))
			return
		}
		response.TemporaryRedirect(ctx, h.setupURL(setupToken, returnTo, ""))

	case "link":
		// Read and immediately clear the Lax link-session cookie (single-use).
		sessVal, _ := ctx.Cookie(oauthLinkSessionCookie)
		ctx.SetSameSite(http.SameSiteLaxMode)
		ctx.SetCookie(oauthLinkSessionCookie, "", -1, "/api/auth", "", true, true)
		if sessVal == "" {
			response.TemporaryRedirect(
				ctx,
				h.hosts.IDURL("/profile?error=link_failed"),
			)
			return
		}
		if err := h.useCase.LinkGoogleToAccountFromOAuth(
			ctx.Request.Context(),
			sessVal,
			code,
			state,
		); err != nil {
			response.TemporaryRedirect(
				ctx,
				h.hosts.IDURL("/profile?error=link_failed"),
			)
			return
		}
		response.TemporaryRedirect(
			ctx,
			h.hosts.IDURL("/profile?tab=connections"),
		)

	default: // sign-in
		cookie, redirect, err := h.useCase.GoogleAuth(ctx.Request.Context(), code, state, meta)
		if err != nil {
			switch {
			case errors.Is(err, authModel.ErrAuthGoogleNotRegistered.Err()):
				h.googleErrorRedirect(ctx, "/sign-up", "not_registered", redirect)
			case errors.Is(err, authModel.ErrAuthAccountBlocked.Err()):
				h.googleErrorRedirect(ctx, "/sign-in", "blocked", redirect)
			default:
				h.googleErrorRedirect(ctx, "/sign-in", "failed", redirect)
			}
			return
		}
		h.prot.Authenticate(ctx, cookie)
		response.TemporaryRedirect(ctx, redirect)
	}
}

// googleLinkRedirect godoc
// @Summary  Begin linking Google to the authenticated account (redirects to Google OAuth)
// @Tags     auth
// @Success  307
// @Failure  401  {object}  response.Response
// @Failure  500  {object}  response.Response
// @Router   /auth/google/link [get]
// googleLinkRedirect starts linking Google to the signed-in account.
func (h *Handler) googleLinkRedirect(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees an authenticated caller.
	url, state, err := h.useCase.GetGoogleLoginURL("")
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	// The session cookie is SameSite=Strict and will NOT be sent on the
	// cross-site top-level navigation that Google redirects back to. Stash it
	// now (this request is same-site) in a short-lived Lax cookie so the
	// callback can read it.
	sessVal, _ := ctx.Cookie(authModel.SessionCookie)
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(oauthIntentCookie, "link", h.oauthCookieMaxAge, "/api/auth", "", true, true)
	ctx.SetCookie(oauthStateCookie, state, h.oauthCookieMaxAge, "/api/auth", "", true, true)
	ctx.SetCookie(oauthLinkSessionCookie, sessVal, h.oauthCookieMaxAge, "/api/auth", "", true, true)
	response.TemporaryRedirect(ctx, url)
}

// unlinkGoogle godoc
// @Summary  Unlink Google from the authenticated user's account
// @Tags     auth
// @Produce  json
// @Success  200
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/google/link [delete]
func (h *Handler) unlinkGoogle(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if err := h.useCase.UnlinkGoogle(ctx.Request.Context(), userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// setupURL builds the absolute id-frontend setup link, with optional return_to
// and error query params.
func (h *Handler) setupURL(setupToken, returnTo, errCode string) string {
	u := h.hosts.IDURL("/setup?token=" + url.QueryEscape(setupToken))
	if errCode != "" {
		u += "&error=" + url.QueryEscape(errCode)
	}
	if returnTo != "" {
		u += "&return_to=" + url.QueryEscape(returnTo)
	}
	return u
}

// googleErrorRedirect sends the browser (googleCallback is a top-level navigation)
// to the id frontend auth UI with a google_error code, instead of rendering raw
// JSON. Portless https, on ID_HOST.
func (h *Handler) googleErrorRedirect(ctx *gin.Context, path, code, returnTo string) {
	u := h.hosts.IDURL(path + "?google_error=" + url.QueryEscape(code))
	if returnTo != "" {
		u += "&return_to=" + url.QueryEscape(returnTo)
	}
	response.TemporaryRedirect(ctx, u)
}
