package proxy

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"net/http/httputil"
	"net/url"
	"strings"
)

type (
	proxyHandler struct {
		config  *config.ProxyConfig
		useCase IUseCase
	}

	IUseCase interface {
		ShouldProxyEvent(ctx context.Context, tag string) bool
	}

	Dependencies struct {
		Config  *config.ProxyConfig
		UseCase IUseCase
	}
)

// HandleProxyToMainPages returns a handler that proxies requests to the sign-in and profile pages of the main frontend
func HandleProxyToMainPages() gin.HandlerFunc {
	pages := []string{"/sign-in", "/profile"}
	startsWithPages := func(url string) bool {
		for _, page := range pages {
			if strings.HasPrefix(url, page) {
				return true
			}
		}
		return false
	}

	return func(ctx *gin.Context) {
		subDomain := ctx.GetString(tools.SubdomainCtxKey)
		if subDomain != "" {
			if startsWithPages(ctx.Request.URL.Path) {
				protection.SetFromURL(ctx, ctx.Request.Referer())
				protection.RedirectToMainDomainPage(ctx, ctx.Request.URL.Path)
			}
		}
	}
}

// HandleProxy returns a handler that proxies requests to the target URL
func HandleProxy(deps Dependencies) gin.HandlerFunc {
	p := &proxyHandler{
		config:  deps.Config,
		useCase: deps.UseCase,
	}
	return func(ctx *gin.Context) {
		target, err := p.getTarget(ctx)

		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}

		if target == "" {
			// if target is empty, event is not found, so redirect to event not found page
			protection.RedirectToMainDomainPage(ctx, config.EventNotFoundPage)
			return
		}

		if targetUrl, err := url.Parse(target); err != nil {
			response.AbortWithError(ctx, err)
			return
		} else {
			proxy(targetUrl).ServeHTTP(ctx.Writer, ctx.Request)
		}
	}
}

// getTarget returns the target URL for the proxy
func (p *proxyHandler) getTarget(ctx *gin.Context) (string, error) {
	// get the subdomain from the context
	destSubdomain, exists := ctx.Get(tools.SubdomainCtxKey)

	if !exists {
		return "", model.ErrPlatformSubdomainNotFoundInContext.Cause()
	}

	switch destSubdomain.(string) {
	case config.MainSubdomain:
		return p.config.MainFrontend, nil
	case config.AdminSubdomain:
		return p.config.AdminFrontend, nil
	default:
		if p.useCase.ShouldProxyEvent(ctx, destSubdomain.(string)) {

			// set the subdomain to header for the event frontend
			ctx.Request.Header.Set(tools.SubdomainCtxKey, destSubdomain.(string))

			return p.config.EventFrontend, nil
		}
		return "", nil
	}
}

func proxy(address *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(address)
	return p
}
