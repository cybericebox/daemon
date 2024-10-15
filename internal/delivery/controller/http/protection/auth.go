package protection

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
	"strings"
)

type (
	IAuthProtectionUseCase interface {
		GetCurrentUserRole(ctx context.Context) (string, error)
		RefreshTokensAndReturnUserID(ctx context.Context, oldTokens model.Tokens) *model.CheckTokensResult
	}
)

func RequireProtection(withRedirect ...bool) gin.HandlerFunc {
	redirect := false
	if len(withRedirect) > 0 {
		redirect = withRedirect[0]
	}
	return func(ctx *gin.Context) {
		if authenticated := protector.authenticateUser(ctx, redirect); authenticated {
			protector.checkDomainPermissions(ctx, redirect)
		}
	}
}

func DynamicallyRequireProtection(needProtection func(ctx *gin.Context) bool, withRedirect ...bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if needProtection(ctx) {
			RequireProtection(withRedirect...)(ctx)
		}
	}
}

// checkDomainPermissions checks if user has permissions to get domain resources
func (p *protection) checkDomainPermissions(ctx *gin.Context, redirectOnUnauthorized bool) {
	role, err := protector.useCase.GetCurrentUserRole(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	// set user role to context
	ctx.Set(tools.UserRoleCtxKey, role)

	// administrative interface
	if ctx.GetString(tools.SubdomainCtxKey) == config.AdminSubdomain {
		if role != model.AdministratorRole {
			// if user is not an admin redirect to main domain page
			p.unauthorizedResponse(ctx, redirectOnUnauthorized)
			return
		}
	}
}

// ValidateRequestDomain validates if the request domain is equal to the domain of the platform
func ValidateRequestDomain(ctx *gin.Context) {
	if !strings.HasSuffix(ctx.Request.Host, config.PlatformDomain) {
		response.AbortWithNotFound(ctx)
		return
	}
	// get subdomain if exists (e.g. event.domain.com -> event, domain.com -> "")
	subdomain := strings.TrimSuffix(strings.TrimSuffix(ctx.Request.Host, config.PlatformDomain), ".")
	ctx.Set(tools.SubdomainCtxKey, subdomain)
}

// authenticateUser authenticate user by tokens
func (p *protection) authenticateUser(ctx *gin.Context, redirectOnUnauthenticated bool) bool {
	// get current tokens
	currentTokens := p.getTokens(ctx)

	// if tokens are empty return unauthorized response
	if currentTokens.AccessToken == "" && currentTokens.RefreshToken == "" {
		p.unauthenticatedResponse(ctx, redirectOnUnauthenticated)
		return false
	}

	result := p.useCase.RefreshTokensAndReturnUserID(ctx, currentTokens)

	// if tokens are not valid return unauthorized response
	if !result.Valid {
		p.unauthenticatedResponse(ctx, redirectOnUnauthenticated)
		return false
	}

	// if valid set user id to context and set tokens to cookies
	ctx.Set(tools.UserIDCtxKey, result.UserID)

	// if tokens were refreshed set new tokens to cookies
	if result.Refreshed {
		p.setTokens(ctx, result.Tokens)
	}

	return true
}

// unauthenticatedResponse returns unauthorized response for api or redirects to sign in page
func (p *protection) unauthenticatedResponse(ctx *gin.Context, redirectOnUnauthenticated bool) {
	// if redirectOnUnauthenticated is false
	if !redirectOnUnauthenticated {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	// save "from" url to cookie
	SetFromURL(ctx)
	// redirect to sign in page
	RedirectToMainDomainPage(ctx, config.SignInPage)
	ctx.Abort()
}

func (p *protection) unauthorizedResponse(ctx *gin.Context, redirectOnUnauthorized bool) {
	// if redirectOnUnauthorized is false
	if !redirectOnUnauthorized {
		response.AbortWithForbidden(ctx)
		return
	}

	// if user not on root path redirect to root
	if ctx.Request.URL.Path != "/" {
		response.TemporaryRedirect(ctx, "/")
	} else {
		// if user already on root path then redirect to main domain and not in main domain root path
		if ctx.GetString(tools.SubdomainCtxKey) != "" {
			RedirectToMainDomainPage(ctx)
		}
	}
}

// RedirectToMainDomainPage redirects to main domain page with status
func RedirectToMainDomainPage(ctx *gin.Context, page ...string) {
	// get page name
	pagePath := "/"
	if len(page) > 0 {
		pagePath = page[0]
	}
	// get main domain url
	mainDomainURL := fmt.Sprintf("%s://%s%s", config.SchemeHTTPS, config.PlatformDomain, pagePath)

	// redirect to main domain page
	response.TemporaryRedirect(ctx, mainDomainURL)
}

// GetFromURL get "from" url from cookie and delete it
func GetFromURL(ctx *gin.Context) (value string) {
	// get cookie value to return it
	value, err := ctx.Cookie(config.FromURLField)
	if err != nil || value == "" {
		if err == nil {
			err = errors.New("no value found")
		}
		log.Debug().Err(err).Str("Cookie", config.FromURLField).Msg("can not get cookie from context")
		value = config.DefaultFromURL
	}
	// set cookie with -1 ttl to delete it
	ctx.SetCookie(config.FromURLField, "", -1, "/", config.PlatformDomain, true, false)
	return
}

// SetFromURL save "from" url to cookie
func SetFromURL(ctx *gin.Context, from ...string) {
	fromURL := fmt.Sprintf("%s://%s%s", config.SchemeHTTPS, ctx.Request.Host, ctx.Request.URL.String())

	if len(from) > 0 {
		fromURL = from[0]
	}

	if fromURL != "" {
		ctx.SetCookie(config.FromURLField, fromURL, int(protector.config.TemporalCookieTTL.Seconds()), "/", config.PlatformDomain, true, false)
	}
}

func SetAuthenticated(ctx *gin.Context, tokens *model.Tokens, redirect ...bool) {
	// set tokens to cookies
	protector.setTokens(ctx, tokens)

	// if redirect is true
	if len(redirect) > 0 && redirect[0] {
		// get from url if user was redirected to sign in page
		from := GetFromURL(ctx)
		// redirect to "from" url
		response.TemporaryRedirect(ctx, from)
	}

}

func DeAuthenticate(ctx *gin.Context) {
	protector.unsetTokens(ctx)
	response.AbortWithSuccess(ctx)
}

func (p *protection) getTokens(ctx *gin.Context) model.Tokens {
	accessToken, err := ctx.Cookie(model.AccessToken)
	if err != nil {
		log.Debug().Err(err).Msg("Cannot get access token from cookie")
	}

	refreshToken, err := ctx.Cookie(model.RefreshToken)
	if err != nil {
		log.Debug().Err(err).Msg("Cannot get refresh token from cookie")
	}

	return model.Tokens{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}
}

// setTokens unsets tokens from cookies
func (p *protection) setTokens(ctx *gin.Context, tokens *model.Tokens) {
	ctx.SetCookie(model.AccessToken, tokens.AccessToken, int(p.config.JWT.AccessTokenTTL.Seconds()), "/", config.PlatformDomain, true, true)
	ctx.SetCookie(model.RefreshToken, tokens.RefreshToken, int(p.config.JWT.RefreshTokenTTL.Seconds()), "/", config.PlatformDomain, true, true)
	ctx.SetCookie(model.PermissionsToken, tokens.PermissionsToken, int(p.config.JWT.RefreshTokenTTL.Seconds()), "/", config.PlatformDomain, true, false)
}

// unsetTokens unsets tokens from cookies
func (p *protection) unsetTokens(ctx *gin.Context) {
	ctx.SetCookie(model.AccessToken, "", -1, "/", config.PlatformDomain, true, true)
	ctx.SetCookie(model.RefreshToken, "", -1, "/", config.PlatformDomain, true, true)
	ctx.SetCookie(model.PermissionsToken, "", -1, "/", config.PlatformDomain, true, false)
}
