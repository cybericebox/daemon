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
	// OpenSession decrypts the cookie, checks its expiry and the revocation set; it never touches the database.
	OpenSession(cookieValue string) (authUseCase.SessionPass, error)
	// LoadCaller is the one query of a request: the caller's global role and blocked flag.
	LoadCaller(ctx context.Context, pass authUseCase.SessionPass) (authModel.AuthClaims, error)
	// ReissueCookie returns a new cookie once 1% of the idle TTL has passed since this one was issued.
	ReissueCookie(pass authUseCase.SessionPass) (string, bool)
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

// cookieLifetime is how long the browser keeps the session cookie: the idle TTL, which every re-issue starts
// again (the expiry inside the cookie is what the server enforces).
func cookieLifetime(cfg config.AuthConfig) time.Duration {
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
		if !p.authenticate(ctx, silentAuthCheck) {
			// authenticate already aborted (hard-401 case, or the limiter) — the
			// deferred global error handler will write the response; don't
			// let the permission check below run and race it with a 403.
			return
		}

		_, authenticated := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
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

// authenticate resolves the caller of a request in the order of the session spec, so a dead cookie costs no
// database work and a flood of a user's requests stops before the first query:
//
//  1. decrypt the cookie and check its expiry; 2. look the session up in the in-memory revocation set (garbage,
//     expired or revoked: 401, no database); 3. the per-user rate limit; 4. one query for the caller's role and
//     blocked flag.
//
// On success it injects the identity into the request context and re-issues the cookie when due. silent controls
// the failure behavior: false (the permission requires authentication) aborts with the error's own status and the
// X-Sign-In-URL header so the client can redirect; true (the permission is public-optional) lets the request
// continue as an anonymous one. It reports false when the request was aborted.
func (p *Protection) authenticate(ctx *gin.Context, silent bool) bool {
	cookie := getCookie(ctx, authModel.SessionCookie)
	if cookie == "" {
		if silent {
			return p.countAnonymous(ctx)
		}
		return p.refuse(ctx, authModel.ErrAuthMissingSessionCookie.Err())
	}
	pass, err := p.useCase.OpenSession(cookie)
	if err != nil {
		return p.failed(ctx, err, silent)
	}
	if p.limiter != nil && !p.limiter.Check(ctx, pass.Ticket.UserID, true) {
		return false
	}
	claims, err := p.useCase.LoadCaller(ctx.Request.Context(), pass)
	if err != nil {
		if silent {
			return p.failedSilently(ctx, err)
		}
		return p.failed(ctx, err, false)
	}
	ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), claims))
	if value, ok := p.useCase.ReissueCookie(pass); ok {
		setCookie(ctx, authModel.SessionCookie, value, http.SameSiteStrictMode, p.ttl)
	}
	return true
}

// countAnonymous counts a request without identity in the anonymous bucket.
func (p *Protection) countAnonymous(ctx *gin.Context) bool {
	return p.limiter == nil || p.limiter.Check(ctx, uuid.Nil, false)
}

// failed handles a cookie that did not pass: a dead cookie is cleared; a silent route goes on anonymous, any
// other aborts with the error.
func (p *Protection) failed(ctx *gin.Context, err error, silent bool) bool {
	if isUnauthorized(err) {
		p.clearSessionCookie(ctx)
	}
	if silent {
		return p.failedSilently(ctx, err)
	}
	return p.refuse(ctx, err)
}

func (p *Protection) failedSilently(ctx *gin.Context, err error) bool {
	log.Debug().Err(err).Caller().Msg("Silent auth check failed")
	return p.countAnonymous(ctx)
}

// refuse aborts the request with err, advertising the sign-in URL on a 401 so the client can redirect without
// computing the address itself.
func (p *Protection) refuse(ctx *gin.Context, err error) bool {
	if isUnauthorized(err) {
		ctx.Header(SignInURLHeader, p.signInURL())
	}
	response.AbortWithError(ctx, err)
	return false
}

func isUnauthorized(err error) bool {
	var classified appErr.Error
	return errors.As(err, &classified) && classified.StatusCode().HTTPCode() == http.StatusUnauthorized
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
