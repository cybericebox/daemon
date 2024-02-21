package proxy

import (
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httputil"
	"net/url"
)

type (
	proxyHandler struct {
		config  *config.ProxyConfig
		service Service
	}

	Service interface {
	}

	Dependencies struct {
		Config  *config.ProxyConfig
		Service Service
	}
)

func HandleProxy(deps Dependencies) gin.HandlerFunc {
	p := &proxyHandler{
		config:  deps.Config,
		service: deps.Service,
	}
	return func(ctx *gin.Context) {
		target, err := p.getTarget(ctx)

		if err != nil {
			response.LogAndAbortWithInternalServerError(ctx, err)
			return
		}

		if target == "" {
			response.AbortWithNotFound(ctx)
			return
		}

		if targetUrl, err := url.Parse(target); err != nil {
			response.LogAndAbortWithInternalServerError(ctx, err)
			return
		} else {
			proxy(targetUrl).ServeHTTP(ctx.Writer, ctx.Request)
		}
	}
}

func (p *proxyHandler) getTarget(ctx *gin.Context) (string, error) {
	return "", nil

}

func proxy(address *url.URL) *httputil.ReverseProxy {
	p := httputil.NewSingleHostReverseProxy(address)

	p.ModifyResponse = func(response *http.Response) error {
		//
		return nil
	}

	return p
}
