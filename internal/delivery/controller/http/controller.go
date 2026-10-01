package http

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler"
	_ "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/apidocs"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

type (
	Controller struct {
		server *Server
	}

	IUseCase interface {
		// IUseCase is dependencies for the http handler
		handler.IUseCase
		protection.IUseCase
	}

	Dependencies struct {
		UseCase    IUseCase
		Config     *config.HTTPControllerConfig
		AuthConfig config.AuthConfig
	}
)

func NewController(deps Dependencies) *Controller {
	// create the router
	// gin.New, not gin.Default: logger and recovery come from ForMode, per the
	// gin mode config.SetupLogger set from ENV (and so does the route dump).
	router := gin.New()
	router.Use(middleware.ForMode(gin.Mode())...)

	// add global middleware for error handling
	router.Use(response.WithErrorHandler)
	router.Use(middleware.CaptureRequestReceivedAt())

	// build protection middleware and wire it into the handler aggregator
	prot := protection.New(protection.Dependencies{
		UseCase: deps.UseCase,
		Config:  deps.AuthConfig,
	})

	// This service answers on exactly one host, api.<domain> — every other
	// host is routed to a frontend by the edge proxy and never reaches here.
	router.Use(prot.RequireAPIHost)

	// optionally serve the Swagger UI
	if deps.Config.EnableSwaggerDocs {
		// redirect /docs -> /docs/index.html (no trailing slash: distinct path segment, avoids
		// conflicting with the /docs/*any catch-all in gin's radix tree)
		router.GET("/docs", func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, "/docs/index.html")
		})
		router.GET("/docs/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}

	// Every frontend calls this API cross-origin (its own subdomain) — CORS
	// (with credentials) must run before any route handling.
	router.Use(middleware.HandleCORSMiddleWare(deps.AuthConfig.Domain), middleware.ContentMiddleware)

	RegisterHealth(router)

	// create handler for routes on current service
	handler.NewAPIHandler(deps.UseCase, prot, deps.AuthConfig.Domain).Init(router)

	return &Controller{
		server: NewServer(&deps.Config.Server, router),
	}
}

// RegisterHealth adds GET /api/health — a public liveness probe (no auth, no
// DB). Session-aware frontend recovery probes /api/auth/me instead.
func RegisterHealth(router gin.IRouter) {
	router.GET("/api/health", func(ctx *gin.Context) {
		response.AbortWithSuccess(ctx)
	})
}

func (c *Controller) Start() {
	c.server.Start()
}

func (c *Controller) Stop(ctx context.Context) {
	c.server.Stop(ctx)
}
