package auth

import (
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	passkeyModel "github.com/cybericebox/daemon/internal/model/passkey"
	"github.com/cybericebox/daemon/internal/model/rbac"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

// redirectURLKey names the sign-in answer field the client navigates by.
const redirectURLKey = "RedirectURL"

// passkeyCeremonyResponse starts a ceremony: Options is the WebAuthn options object ({"publicKey": ...}) for
// navigator.credentials.create / get (or PublicKeyCredential.parse*OptionsFromJSON(Options.publicKey)).
type passkeyCeremonyResponse struct {
	CeremonyID string          `json:"CeremonyID"`
	Options    json.RawMessage `json:"Options"`
}

func toPasskeyCeremonyResponse(c authUseCase.PasskeyCeremony) passkeyCeremonyResponse {
	return passkeyCeremonyResponse{CeremonyID: c.CeremonyID, Options: c.Options}
}

type passkeySignInFinishRequest struct {
	CeremonyID string `json:"CeremonyID"`
	// Credential is the PublicKeyCredential the browser returned, as its toJSON() form.
	Credential json.RawMessage `json:"Credential"`
	Redirect   string          `json:"Redirect"`
}

type passkeyRegistrationFinishRequest struct {
	CeremonyID string          `json:"CeremonyID"`
	Name       string          `json:"Name"`
	Credential json.RawMessage `json:"Credential"`
}

type passkeyResponse struct {
	ID             uuid.UUID  `json:"ID"`
	Name           string     `json:"Name"`
	Transports     []string   `json:"Transports"`
	AAGUID         string     `json:"AAGUID"`
	BackupEligible bool       `json:"BackupEligible"`
	BackedUp       bool       `json:"BackedUp"`
	CreatedAt      time.Time  `json:"CreatedAt"`
	LastUsedAt     *time.Time `json:"LastUsedAt"`
	// Compromised is true once the key's counter went backwards; CompromisedAt says when. Such a key cannot sign in.
	Compromised   bool       `json:"Compromised"`
	CompromisedAt *time.Time `json:"CompromisedAt"`
}

func toPasskeyResponse(p passkeyModel.Passkey) passkeyResponse {
	aaguid := ""
	if id, err := uuid.FromBytes(p.AAGUID); err == nil {
		aaguid = id.String()
	}
	transports := p.Transports
	if transports == nil {
		transports = []string{}
	}
	return passkeyResponse{
		ID:             p.ID,
		Name:           p.Name,
		Transports:     transports,
		AAGUID:         aaguid,
		BackupEligible: p.BackupEligible,
		BackedUp:       p.BackupState,
		CreatedAt:      p.CreatedAt,
		LastUsedAt:     p.LastUsedAt,
	}
}

// passkeySignInBegin godoc
// @Summary  Begin a usernameless passkey sign-in
// @Tags     auth
// @Accept   json
// @Produce  json
// @Description  The body carries the captcha token: {"RecaptchaToken": "..."}.
// @Success  200  {object}  response.Response{data=passkeyCeremonyResponse}
// @Failure  400  {object}  response.Response
// @Router   /auth/passkeys/sign-in/begin [post]
func (h *Handler) passkeySignInBegin(ctx *gin.Context) {
	ceremony, err := h.useCase.BeginPasskeySignIn(ctx.Request.Context())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toPasskeyCeremonyResponse(ceremony))
}

// passkeySignInFinish godoc
// @Summary  Finish a passkey sign-in with the authenticator's assertion
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      passkeySignInFinishRequest  true  "ceremony and assertion"
// @Success  200  {object}  response.Response{data=object}
// @Failure  400  {object}  response.Response
// @Failure  401  {object}  response.Response
// @Router   /auth/passkeys/sign-in/finish [post]
func (h *Handler) passkeySignInFinish(ctx *gin.Context) {
	var req passkeySignInFinishRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	cookie, redirect, err := h.useCase.PasskeySignIn(
		ctx.Request.Context(), req.CeremonyID, req.Redirect, req.Credential, sessionMetadata(ctx),
	)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	h.startSession(ctx, cookie)
	response.AbortWithData(ctx, gin.H{redirectURLKey: redirect})
}

// listPasskeys godoc
// @Summary  List the current user's passkeys
// @Tags     auth
// @Produce  json
// @Success  200  {object}  response.Response{data=[]passkeyResponse}
// @Router   /auth/passkeys [get]
func (h *Handler) listPasskeys(ctx *gin.Context) {
	// RequirePermission(rbac.PermSelf) on this route guarantees userID is present.
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	items, err := h.useCase.ListPasskeys(ctx.Request.Context(), claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]passkeyResponse, 0, len(items))
	for _, it := range items {
		out = append(out, toPasskeyResponse(it))
	}
	response.AbortWithData(ctx, out)
}

// passkeyRegistrationBegin godoc
// @Summary  Begin adding a passkey (the owner is confirmed first)
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      reauthRequest  false  "current password (accounts without a password need a recent sign-in instead)"
// @Success  200  {object}  response.Response{data=passkeyCeremonyResponse}
// @Failure  400  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Router   /auth/passkeys/registration/begin [post]
func (h *Handler) passkeyRegistrationBegin(ctx *gin.Context) {
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	req, ok := bindReauth(ctx)
	if !ok {
		return
	}
	ceremony, err := h.useCase.BeginPasskeyRegistration(
		ctx.Request.Context(),
		claims.UserID,
		req.CurrentPassword,
	)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toPasskeyCeremonyResponse(ceremony))
}

// passkeyRegistrationFinish godoc
// @Summary  Finish adding a passkey with the authenticator's attestation
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    body  body      passkeyRegistrationFinishRequest  true  "ceremony, name and attestation"
// @Success  200  {object}  response.Response{data=passkeyResponse}
// @Failure  400  {object}  response.Response
// @Router   /auth/passkeys/registration/finish [post]
func (h *Handler) passkeyRegistrationFinish(ctx *gin.Context) {
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	var req passkeyRegistrationFinishRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	created, err := h.useCase.FinishPasskeyRegistration(
		ctx.Request.Context(), claims.UserID, req.CeremonyID, req.Name, req.Credential,
	)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	audit.SetTarget(ctx, "passkey:"+created.ID.String())
	response.AbortWithData(ctx, toPasskeyResponse(created))
}

// removePasskey godoc
// @Summary  Remove one of the current user's passkeys (the owner is confirmed first; refused when it is the last way to sign in)
// @Tags     auth
// @Accept   json
// @Produce  json
// @Param    id    path  string         true   "passkey ID"
// @Param    body  body  reauthRequest  false  "current password (accounts without a password need a recent sign-in instead)"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /auth/passkeys/{id} [delete]
func (h *Handler) removePasskey(ctx *gin.Context) {
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	req, ok := bindReauth(ctx)
	if !ok {
		return
	}
	if err = h.useCase.RemovePasskey(
		ctx.Request.Context(),
		claims.UserID,
		id,
		req.CurrentPassword,
	); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
