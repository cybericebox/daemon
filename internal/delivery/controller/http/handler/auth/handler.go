package auth

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
		hosts   config.HostsConfig
		// oauthCookieMaxAge is the life of the short OAuth state cookies, seconds (OAUTH_STATE_TTL).
		oauthCookieMaxAge int
	}

	IUseCase interface {
		SignIn(
			ctx context.Context,
			email, password, redirect string,
			meta authModel.SessionMetadata,
		) (string, string, error)
		IsTrustedRedirect(rawURL string) bool
		ListSessions(
			ctx context.Context,
			userID, currentSessionID uuid.UUID,
		) ([]authUseCase.SessionInfo, error)
		RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error
		RevokeOtherSessions(ctx context.Context, userID, currentSessionID uuid.UUID) error
		BeginEmailRegistration(ctx context.Context, email, redirect string) error
		GetSetupContext(ctx context.Context, setupToken string) (*authUseCase.SetupContext, error)
		CompleteRegistration(
			ctx context.Context,
			setupToken, firstName, lastName, password string,
			tosVersion int32,
			redirect string,
			meta authModel.SessionMetadata,
		) (string, string, error)
		PasswordPolicy() authUseCase.PasswordPolicy
		ForgotPassword(ctx context.Context, email string) error
		ResetPassword(ctx context.Context, code, password string) error
		SetAccountPassword(
			ctx context.Context,
			userID uuid.UUID,
			oldPassword, newPassword string,
		) error
		GetGoogleLoginURL(redirect string) (string, string, error)
		GoogleAuth(
			ctx context.Context,
			code, state string,
			meta authModel.SessionMetadata,
		) (string, string, error)
		BeginGoogleRegistration(
			ctx context.Context,
			code, state string,
			meta authModel.SessionMetadata,
		) (authUseCase.GoogleRegistrationResult, error)
		LinkGoogleToSetupFromOAuth(ctx context.Context, setupToken, code, state string) (string, error)

		// slice 4a — account self-management
		GetAccount(ctx context.Context, userID uuid.UUID) (*authUseCase.AccountInfo, error)
		GetSelfProfile(ctx context.Context, userID uuid.UUID) (*authUseCase.UserInfo, error)
		UpdateAccountProfile(
			ctx context.Context,
			userID uuid.UUID,
			firstName, lastName string,
		) error
		RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail, currentPassword string) error
		ConfirmEmailChange(ctx context.Context, code string) error
		UnlinkGoogle(ctx context.Context, userID uuid.UUID) error
		DeleteAccount(ctx context.Context, userID uuid.UUID) error
		LinkGoogleToAccountFromOAuth(
			ctx context.Context,
			sessionCookieValue, code, state string,
		) error

		// avatars (S3-backed)
		UploadAvatar(
			ctx context.Context,
			userID uuid.UUID,
			r io.Reader,
			size int64,
			contentType string,
		) error
		RemoveAvatar(ctx context.Context, userID uuid.UUID) error
		GetAvatar(ctx context.Context, userID uuid.UUID) (io.ReadCloser, string, error)
	}

	// IProtection is the subset of the protection middleware the handler calls.
	IProtection interface {
		Authenticate(ctx *gin.Context, value string)
		DeAuthenticate(ctx *gin.Context)
		RequireRecaptcha(action string) gin.HandlerFunc
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}
)

func NewAuthAPIHandler(useCase IUseCase, prot IProtection, cfg config.AuthConfig) *Handler {
	return &Handler{useCase: useCase, prot: prot, hosts: cfg.Hosts, oauthCookieMaxAge: int(cfg.OAuth.StateTTL.Seconds())}
}

func (h *Handler) Init(public, secured *gin.RouterGroup) {
	pub := public.Group("auth")
	pub.POST("sign-in", h.prot.RequireRecaptcha("signIn"), h.signIn)
	pub.POST("sign-up", h.prot.RequireRecaptcha("signUp"), h.signUp)
	pub.GET("setup", h.getSetup)
	pub.POST("setup", h.completeSetup)

	password := pub.Group("password")
	password.GET("policy", h.passwordPolicy)
	password.POST("reset-request", h.prot.RequireRecaptcha("forgotPassword"), h.requestPasswordReset)
	password.POST("reset", h.resetPassword)
	password.POST("change", h.prot.RequirePermission(rbac.PermSelf), h.changePassword)

	google := pub.Group("google")
	google.GET("", h.googleRedirect)
	google.GET("register", h.googleRegisterRedirect)
	google.GET("setup", h.googleSetupRedirect)
	google.GET("callback", h.googleCallback)

	sec := secured.Group("auth")
	self := h.prot.RequirePermission(rbac.PermSelf)
	sec.POST("sign-out", self, h.prot.DeAuthenticate)
	sec.GET("sessions", self, h.listSessions)
	sec.DELETE("sessions/:id", self, h.revokeSession)
	sec.DELETE("sessions", self, h.revokeOtherSessions)

	// slice 4a — account self-management
	sec.GET("account", self, h.getAccount)
	sec.GET("me", self, h.getSelfProfile)
	sec.PATCH("account/profile", self, h.updateProfile)
	sec.POST("account/email", self, h.requestEmailChange)
	sec.DELETE("account", self, h.deleteAccount)
	sec.POST("account/avatar", self, h.uploadAvatar)
	sec.DELETE("account/avatar", self, h.removeAvatar)
	sec.GET("google/link", self, h.googleLinkRedirect)
	sec.DELETE("google/link", self, h.unlinkGoogle)

	pub.POST("account/email/confirm", h.confirmEmailChange)
	// Public avatar proxy: streams the stored image so the bucket stays private.
	pub.GET("avatar/:id", h.getAvatar)
}
