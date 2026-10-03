package protection

import (
	"context"
	"errors"

	"net/http"
	"strings"
	"sync"
	"time"

	recaptcha "cloud.google.com/go/recaptchaenterprise/v2/apiv1"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/errjournal"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// IUseCase is the auth port the middleware depends on.
type IUseCase interface {
	ValidateSessionCookie(
		ctx context.Context,
		cookieValue string,
	) (*authUseCase.SessionAuthResult, error)
	UpdateLastSeen(ctx context.Context, sessionID, userID uuid.UUID) error
	SignOut(ctx context.Context, sessionID uuid.UUID) error
	RecordAdminAction(ctx context.Context, entry adminAuditUseCase.Entry) error
}

// Limiter is the general request limiter; the gate asks it once the caller is known.
type Limiter interface {
	Check(ctx *gin.Context, userID uuid.UUID, signedIn bool) bool
}

type Protection struct {
	limiter   Limiter
	useCase   IUseCase
	hosts     config.HostsConfig
	ttl       time.Duration
	recaptcha config.RecaptchaConfig
	captcha   CaptchaVerifier

	recaptchaMu     sync.Mutex
	recaptchaClient *recaptcha.Client
}

type Dependencies struct {
	UseCase IUseCase
	Config  config.AuthConfig
	// Limiter counts every gated request in the caller's bucket; nil limits nothing.
	Limiter Limiter
	// Captcha overrides the verifier chosen by Config.Captcha.Provider (tests).
	Captcha CaptchaVerifier
}

func New(deps Dependencies) *Protection {
	p := &Protection{
		limiter:   deps.Limiter,
		useCase:   deps.UseCase,
		hosts:     deps.Config.Hosts,
		ttl:       cookieLifetime(deps.Config),
		recaptcha: deps.Config.Recaptcha,
	}
	p.captcha = deps.Captcha
	if p.captcha == nil {
		p.captcha = p.newCaptchaVerifier(deps.Config)
	}
	return p
}

// cookieLifetime is how long the browser keeps the session cookie: the absolute
// session lifetime (the idle deadline is enforced server-side).
func cookieLifetime(cfg config.AuthConfig) time.Duration {
	if cfg.SessionAbsoluteTTL > 0 {
		return cfg.SessionAbsoluteTTL
	}
	return cfg.SessionIdleTTL
}

// RequireAPIHost rejects any request whose Host is not exactly API_HOST.
// This service answers on a single host now; every other host is routed to a
// frontend by the edge proxy and never reaches this process.
func (p *Protection) RequireAPIHost(ctx *gin.Context) {
	if hostWithoutPort(ctx.Request.Host) != p.hosts.API {
		response.AbortWithNotFound(ctx)
		return
	}
	ctx.Next()
}

// RequirePermission is the single entry point that gates a route: it runs
// checkAuthentication internally (hard-401 if required.RequireAuthentication()
// is true, i.e. the permission is not public-optional; silent otherwise),
// which injects the role into the request context. An unauthenticated caller
// uses RolePublic only for permissions explicitly granted to that role.
func (p *Protection) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	silentAuthCheck := required.RequireAuthentication() == false
	return func(ctx *gin.Context) {
		p.checkAuthentication(ctx, silentAuthCheck)
		if ctx.IsAborted() {
			// checkAuthentication already aborted (hard-401 case) — the
			// deferred global error handler will write the response; don't
			// let the permission check below run and race it with a 403.
			return
		}

		claims, authenticated := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
		if p.limiter != nil && !p.limiter.Check(ctx, claims.UserID, authenticated) {
			return
		}
		if !rbac.HasPermissionInContext(ctx.Request.Context(), required) &&
			(authenticated || !rbac.RolePublic.HasPermission(required)) {
			// The journal records which permission refused: refusals point at wrong permissions.
			errjournal.SetPermission(ctx, string(required))
			response.AbortWithForbidden(ctx)
			// A refused administrative action by a signed-in user is worth a
			// line too (probing shows up as a run of 403s).
			p.recordAudit(ctx, required, http.StatusForbidden)
			return
		}
		ctx.Next()
		p.recordAudit(ctx, required, response.FinalStatus(ctx))
	}
}

// recordAudit writes the audit entry of an administrative action with the
// status the client actually gets (a handler's error is only written by the
// outer error handler, after this middleware returns).
func (p *Protection) recordAudit(ctx *gin.Context, required rbac.Permission, status int) {
	if !isAuditedAction(required, ctx) {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		return
	}
	target := audit.Target(ctx)
	if target == "" {
		target = routeTarget(ctx)
	}
	if err := p.useCase.RecordAdminAction(ctx.Request.Context(), adminAuditUseCase.Entry{
		ActorID: claims.UserID, Permission: string(required), Method: ctx.Request.Method,
		Route: ctx.FullPath(), ResponseStatus: status, Target: target,
	}); err != nil {
		log.Warn().Err(err).Str("route", ctx.FullPath()).Msg("Failed to record admin audit action")
	}
}

// isAuditedAction decides what the admin audit log records:
//   - every write (not GET/HEAD/OPTIONS) under a real permission, except a
//     user's own inbox settings (notifications.self);
//   - PermSelf writes only where PermSelf stands for staff work: the event
//     manage area and the exercise catalog (the plain self-service writes —
//     profile, sessions, flag submissions — are not administration);
//   - GET exports (CSV/ZIP of people and results), which move PII out.
func isAuditedAction(required rbac.Permission, ctx *gin.Context) bool {
	path := ctx.FullPath()
	switch ctx.Request.Method {
	case http.MethodGet, http.MethodHead:
		return strings.Contains(path, "/export")
	case http.MethodOptions:
		return false
	}
	if required == rbac.PermNotificationsSelf {
		return false
	}
	return required != rbac.PermSelf || strings.Contains(path, "/manage/") || strings.Contains(path, "/exercises")
}

// routeTarget names the objects a request addressed from its route ids
// (UUID-valued params named like ids), for handlers that did not say.
// Never request bodies or query strings.
func routeTarget(ctx *gin.Context) string {
	var parts []string
	for _, param := range ctx.Params {
		if !strings.HasSuffix(strings.ToLower(param.Key), "id") {
			continue
		}
		if _, err := uuid.FromString(param.Value); err != nil {
			continue
		}
		parts = append(parts, param.Key+":"+param.Value)
	}
	return strings.Join(parts, " ")
}

// signInURL is the identity app's sign-in page URL (ID_HOST/sign-in).
func (p *Protection) signInURL() string {
	return p.hosts.IDURL(SignInPath)
}

// resolveAuth validates the request's session cookie and returns the identity
// claims. There is exactly one credential now, valid across every frontend
// origin — which caller may do what is enforced by RBAC permissions and the
// CORS/Origin allowlist, not by which cookie is present.
func (p *Protection) resolveAuth(ctx *gin.Context) (authModel.AuthClaims, error) {
	cookie := getCookie(ctx, authModel.SessionCookie)
	if cookie == "" {
		return authModel.AuthClaims{}, authModel.ErrAuthMissingSessionCookie.Err()
	}
	res, err := p.useCase.ValidateSessionCookie(ctx.Request.Context(), cookie)
	if err != nil {
		if isUnauthorized(err) {
			p.clearSessionCookie(ctx)
		}
		return authModel.AuthClaims{}, err
	}
	return res.Claims, nil
}

// checkAuthentication validates the session cookie and, on success, injects
// userID / role / sessionID into the request context (rbac seam). It is not a
// standalone middleware — RequirePermission calls it internally, once per
// request, before checking HasPermissionInContext. silent controls the
// failure behavior: false (the permission requires authentication) aborts
// with the session-validation error's own status (401 for an invalid,
// expired, or revoked session) and the X-Sign-In-URL header so the client
// can redirect; true (the permission is public-optional, i.e. RolePublic
// already covers it) just logs and lets the request continue unauthenticated.
func (p *Protection) checkAuthentication(ctx *gin.Context, silent bool) {
	claims, err := p.resolveAuth(ctx)
	if err != nil {
		if silent {
			log.Debug().Err(err).Caller().Msg("Silent auth check failed")
			return
		}
		// Advertise the sign-in URL so the client can redirect on 401 without
		// computing the address itself (harmless on the success path).
		if isUnauthorized(err) {
			ctx.Header(SignInURLHeader, p.signInURL())
		}
		response.AbortWithError(ctx, err)
		return
	}
	p.injectContext(ctx, claims)
}

func isUnauthorized(err error) bool {
	var classified appErr.Error
	return errors.As(err, &classified) && classified.StatusCode().HTTPCode() == http.StatusUnauthorized
}

// injectContext writes identity into the request context and fires an async last-seen touch.
func (p *Protection) injectContext(ctx *gin.Context, claims authModel.AuthClaims) {
	ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), claims))
	p.touchAsync(claims.SessionID, claims.UserID)
}

func (p *Protection) touchAsync(sessionID, userID uuid.UUID) {
	go func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := p.useCase.UpdateLastSeen(c, sessionID, userID); err != nil {
			log.Debug().
				Err(err).
				Str("sessionID", sessionID.String()).
				Str("userID", userID.String()).
				Msg("Failed to update last_seen")
		}
	}()
}

// ── cookie helpers ─────────────────────────────────────────────────────────

// Authenticate sets the platform session cookie. SameSite=Strict: every
// frontend origin is same-site with api.<domain>, so Strict cookies are sent
// on ordinary same-site fetches/navigations; the one case they are withheld —
// Google's cross-site redirect back — is handled by the OAuth link flow's own
// short-lived stash cookie (see handler/auth/google.go).
func (p *Protection) Authenticate(ctx *gin.Context, value string) {
	setCookie(ctx, authModel.SessionCookie, value, http.SameSiteStrictMode, p.ttl)
}

func (p *Protection) clearSessionCookie(ctx *gin.Context) {
	clearCookie(ctx, authModel.SessionCookie, http.SameSiteStrictMode)
}

// DeAuthenticate clears the session cookie and deletes the session. Reads the
// session id from the request context (populated by checkAuthentication, via
// RequirePermission) to delete the session.
func (p *Protection) DeAuthenticate(ctx *gin.Context) {
	if authSession, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); ok {
		if err := p.useCase.SignOut(ctx.Request.Context(), authSession.SessionID); err != nil {
			log.Warn().Err(err).Caller().Msg("Failed to sign out session")
		}
	}
	p.clearSessionCookie(ctx)
	response.AbortWithSuccess(ctx)
}

// helpers

func hostWithoutPort(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}

func getCookie(ctx *gin.Context, name string) string {
	value, err := ctx.Cookie(name)
	if err != nil || value == "" {
		return ""
	}
	return value
}

func setCookie(ctx *gin.Context, name, value string, sameSite http.SameSite, ttl time.Duration) {
	ctx.SetSameSite(sameSite)
	ctx.SetCookie(name, value, int(ttl.Seconds()), "/", "", true, true)
}

func clearCookie(ctx *gin.Context, name string, sameSite http.SameSite) {
	ctx.SetSameSite(sameSite)
	ctx.SetCookie(name, "", -1, "/", "", true, true)
}
