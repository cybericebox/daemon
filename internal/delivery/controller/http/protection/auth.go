package protection

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"net/http"
	"strings"
)

type (
	IAuthProtectionUseCase interface {
		GetCurrentUserRole(ctx context.Context) (string, error)
		RefreshTokensIfNeedAndReturnUserID(ctx context.Context, oldTokens model.Tokens) (*model.Tokens, *uuid.UUID, bool, bool)
	}
)

func RequireProtection(ctx *gin.Context) {
	protector.identifyUser(ctx)
	protector.checkPermissions(ctx)
}

func DynamicallyRequireProtection(needAuthentication func(ctx *gin.Context) bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if needAuthentication(ctx) {
			RequireProtection(ctx)
		}
	}
}

func (p *protection) checkPermissions(ctx *gin.Context) {
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
			RedirectToMainDomainPage(ctx, http.StatusTemporaryRedirect)
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

// identifyUser identifies user by tokens
func (p *protection) identifyUser(ctx *gin.Context) {
	currentTokens := p.getTokens(ctx)

	tokens, userID, valid, refreshed := p.useCase.RefreshTokensIfNeedAndReturnUserID(ctx, model.Tokens{
		AccessToken:  currentTokens.AccessToken,
		RefreshToken: currentTokens.RefreshToken,
	})

	// if tokens are not valid return unauthorized response
	if !valid {
		p.unauthorizedResponse(ctx)
		return
	}

	// if valid set user id to context and set tokens to cookies
	ctx.Set(tools.UserIDCtxKey, userID)
	// if tokens were refreshed set new tokens to cookies
	if refreshed {
		p.setTokens(ctx, tokens)
	}
}

// unauthorizedResponse returns unauthorized response for api and redirects to sign in page for web
func (p *protection) unauthorizedResponse(ctx *gin.Context) {
	// save "from" url to cookie
	p.setFromURL(ctx)
	// redirect to sign in page
	RedirectToMainDomainPage(ctx, http.StatusTemporaryRedirect, config.SignInPage)
}

// setFromURL save "from" url
func (p *protection) setFromURL(ctx *gin.Context, from ...string) {
	fromURL := fmt.Sprintf("%s://%s%s", config.SchemeHTTPS, ctx.Request.Host, ctx.Request.URL.String())

	if len(from) > 0 {
		fromURL = from[0]
	}

	if fromURL != "" {
		ctx.SetCookie(config.FromURLField, fromURL, int(p.config.TemporalCookieTTL.Seconds()), "/", config.PlatformDomain, true, false)
	}
}

// RedirectToMainDomainPage redirects to main domain page with status
func RedirectToMainDomainPage(ctx *gin.Context, status int, page ...string) {
	// get page name
	pageName := "/"
	if len(page) > 0 {
		pageName = page[0]
	}
	// get main domain url
	mainDomainURL := fmt.Sprintf("%s://%s%s", config.SchemeHTTPS, config.PlatformDomain, pageName)

	// redirect to main domain page
	response.Redirect(ctx, status, mainDomainURL)
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
	ctx.SetCookie(config.FromURLField, "", -1, "/", config.PlatformDomain, true, false)
	return
}

// SetFromURL saves "from" url to cookie
func SetFromURL(ctx *gin.Context, from ...string) {
	protector.setFromURL(ctx, from...)
}

func SetAuthenticated(ctx *gin.Context, tokens *model.Tokens) {
	// set tokens to cookies
	protector.setTokens(ctx, tokens)

	// get from url if user was redirected to sign in page
	from := GetFromURL(ctx)
	// redirect to "from" url
	response.Redirect(ctx, http.StatusFound, from)

}

func DeAuthenticateAndAbortWithOk(ctx *gin.Context) {
	protector.unsetTokens(ctx)
	response.AbortWithOK(ctx, "Signed out")
}

func (p *protection) getTokens(ctx *gin.Context) model.Tokens {
	accessToken, err := ctx.Cookie(model.AccessToken)
	if err != nil {
		return model.Tokens{}
	}

	refreshToken, err := ctx.Cookie(model.RefreshToken)
	if err != nil {
		return model.Tokens{}
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
