package protection

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/auth"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"strings"
)

type (
	routesProtection struct {
		config  *config.AuthConfig
		scheme  string
		service Service
	}
	Service interface {
		ValidateAccessToken(ctx context.Context, accessToken string) (string, bool)
		RefreshTokens(refreshToken string) (*auth.Tokens, error)
	}

	// Dependencies for the routes protection
	Dependencies struct {
		Config  *config.AuthConfig
		Service Service
	}
)

var routesProtector *routesProtection

func InitRoutesProtection(deps *Dependencies) {

	// set http or https scheme
	scheme := "http"
	if deps.Config.Secure {
		scheme = "https"
	}

	routesProtector = &routesProtection{service: deps.Service, config: deps.Config, scheme: scheme}
}

// ProtectRouteWithAuthentication protects route with authentication
func ProtectRouteWithAuthentication(ctx *gin.Context) {
	routesProtector.identifyUser(ctx)
}

// DynamicallyProtectRouteWithAuthentication dynamically protects route with authentication
func DynamicallyProtectRouteWithAuthentication(ctx *gin.Context) {
	if routesProtector.isRouteNeedProtection(ctx) {
		routesProtector.identifyUser(ctx)
	}
}

// routesProtection checks if route needs protection
func (rp *routesProtection) isRouteNeedProtection(ctx *gin.Context) bool {
	return strings.HasPrefix(ctx.Request.RequestURI, "/profile")
}

// identifyUser identifies user by tokens
func (rp *routesProtection) identifyUser(ctx *gin.Context) {
	userId := rp.validateTokens(ctx)
	ctx.Set(config.UserIdCtxKey, userId)
}

// validateTokens validates tokens and tries to refresh them if needed
func (rp *routesProtection) validateTokens(ctx *gin.Context) (userId string) {
	accessToken, err := ctx.Cookie(config.AccessToken)
	if err != nil {
		log.Error().Err(err).Msg("can not get accessToken from cookie")
		rp.tryRefreshTokens(ctx)

		accessToken = ctx.GetString(config.AccessToken)
	}

	userId, valid := rp.service.ValidateAccessToken(ctx, accessToken)

	if !valid {
		rp.unauthorizedResponse(ctx)
		return
	}

	return
}

// tryRefreshTokens tries to refresh tokens if accessToken is not valid
func (rp *routesProtection) tryRefreshTokens(ctx *gin.Context) {
	refreshToken, err := ctx.Cookie(config.RefreshToken)
	if err != nil {
		log.Error().Err(err).Msg("can not get refreshToken from cookie")
		rp.unauthorizedResponse(ctx)
		return
	}

	tokens, err := rp.service.RefreshTokens(refreshToken)
	if err != nil {
		log.Error().Err(err).Msg("can not parse refreshToken")
		rp.unauthorizedResponse(ctx)
		return
	}
	SetTokens(ctx, tokens)
	ctx.Set(config.AccessToken, tokens.AccessToken)
}

// unauthorizedResponse returns unauthorized response for api and redirects to sign in page for web
func (rp *routesProtection) unauthorizedResponse(ctx *gin.Context) {
	if strings.HasPrefix(ctx.Request.RequestURI, "/api") {
		response.AbortWithUnauthorized(ctx, nil)
		return
	}

	rp.saveFromURL(ctx)

	// get sign in page url
	signInPageURL := fmt.Sprintf("%s://%s/%s", rp.scheme, rp.config.Domain, config.SignInPage)

	response.TemporalRedirect(ctx, signInPageURL)
}

// saveFromURL save "from" url
func (rp *routesProtection) saveFromURL(ctx *gin.Context) {
	scheme := "https"
	if ctx.Request.TLS == nil {
		scheme = "http"
	}

	// set "from" url to redirect back after successful sign in
	fromURL := fmt.Sprintf("%s://%s%s", scheme, ctx.Request.Host, ctx.Request.URL.Path)
	setFromURL(ctx, fromURL)
}

// setFromURL set "from" url to cookie
func setFromURL(ctx *gin.Context, value string) {
	if value != "" {
		ctx.SetCookie(config.FromURLField, value, int(routesProtector.config.TemporalCookieTTL.Seconds()), "/", routesProtector.config.Domain, routesProtector.config.Secure, false)
	}
}

// GetFromURL get "from" url from cookie and delete it
func GetFromURL(ctx *gin.Context) (value string) {
	// get cookie value to return it
	value, err := ctx.Cookie(config.FromURLField)
	if err != nil || value == "" {
		log.Error().Err(err).Str("Cookie", config.FromURLField).Msg("can not get cookie from context")
		value = config.DefaultFromURL
	}
	// set cookie with -1 ttl to delete it
	ctx.SetCookie(config.FromURLField, "", -1, "/", routesProtector.config.Domain, routesProtector.config.Secure, false)
	return
}

// SetTokens sets tokens to cookies
func SetTokens(ctx *gin.Context, tokens *auth.Tokens) {
	ctx.SetCookie(config.AccessToken, tokens.AccessToken, int(routesProtector.config.JWT.AccessTokenTTL.Seconds()), "/", routesProtector.config.Domain, routesProtector.config.Secure, true)
	ctx.SetCookie(config.RefreshToken, tokens.RefreshToken, int(routesProtector.config.JWT.RefreshTokenTTL.Seconds()), "/", routesProtector.config.Domain, routesProtector.config.Secure, true)
	ctx.SetCookie(config.PermissionsToken, tokens.PermissionsToken, int(routesProtector.config.JWT.RefreshTokenTTL.Seconds()), "/", routesProtector.config.Domain, routesProtector.config.Secure, false)
}

// UnsetTokens unsets tokens from cookies
func UnsetTokens(ctx *gin.Context) {
	ctx.SetCookie(config.AccessToken, "", -1, "/", routesProtector.config.Domain, routesProtector.config.Secure, true)
	ctx.SetCookie(config.RefreshToken, "", -1, "/", routesProtector.config.Domain, routesProtector.config.Secure, true)
	ctx.SetCookie(config.PermissionsToken, "", -1, "/", routesProtector.config.Domain, routesProtector.config.Secure, false)
}
