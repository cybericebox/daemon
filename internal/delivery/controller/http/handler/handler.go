package handler

import (
	"context"
	"time"

	adminAuditHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/adminAudit"
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventAnalyticsHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/eventAnalytics"
	eventselfHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/eventself"
	exerciseHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	infrastructureHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/infrastructure"
	mailHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/mail"
	messagingHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/messaging"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification"
	platformAnalyticsHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/platformAnalytics"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/platformSettings"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/user"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type (
	Handler struct {
		useCase IUseCase
		prot    Protector
		cfg     config.AuthConfig
	}

	// IUseCase is the union of every sub-handler's useCase port. The aggregate
	// useCase satisfies it structurally.
	IUseCase interface {
		authHandler.IUseCase
		platformSettings.IUseCase
		notification.IUseCase
		messagingHandler.IUseCase
		user.IUseCase
		exerciseHandler.IUseCase
		eventHandler.IUseCase
		eventselfHandler.IUseCase
		eventAnalyticsHandler.IUseCase
		platformAnalyticsHandler.IUseCase
		infrastructureHandler.IUseCase
		adminAuditHandler.IUseCase
		mailHandler.IUseCase

		// ResolveEventByTag backs the participant-route tenant-resolution
		// middleware (see Init) — the aggregator builds
		// middleware.ResolveEventTenant from this and h.domain, rather than
		// threading the middleware in from controller.go.
		ResolveEventByTag(ctx context.Context, tag string, now time.Time) (eventUseCase.EventTenantView, error)
	}

	// Protector is the subset of the protection middleware the handler aggregator needs.
	Protector interface {
		Authenticate(ctx *gin.Context, value string)
		DeAuthenticate(ctx *gin.Context)
		RequireRecaptcha(action string) gin.HandlerFunc
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}
)

func NewAPIHandler(useCase IUseCase, prot Protector, cfg config.AuthConfig) *Handler {
	return &Handler{useCase: useCase, prot: prot, cfg: cfg}
}

func (h *Handler) Init(router *gin.Engine) {
	baseAPI := router.Group("api")
	{
		platformSettings.NewSettingAPIHandler(h.useCase, h.prot).Init(baseAPI)
		adminAuditHandler.New(h.useCase, h.prot).Init(baseAPI)

		user.NewUserAPIHandler(h.useCase, h.prot).Init(baseAPI)

		notification.NewNotificationsAPIHandler(h.useCase, h.prot).Init(baseAPI)
		messagingHandler.NewMessagingAPIHandler(h.useCase, h.prot).Init(baseAPI)

		exerciseHandler.NewExerciseAPIHandler(h.useCase, h.prot).Init(baseAPI)

		eventHandler.NewEventAPIHandler(h.useCase, h.prot).Init(baseAPI)
		eventAnalyticsHandler.NewEventAnalyticsAPIHandler(h.useCase, h.prot).Init(baseAPI)
		platformAnalyticsHandler.NewPlatformAnalyticsAPIHandler(h.useCase, h.prot).Init(baseAPI)

		mailHandler.NewMailAPIHandler(h.useCase, h.prot).Init(baseAPI)

		infrastructureHandler.NewInfrastructureAPIHandler(h.useCase, h.prot).Init(baseAPI)

		// Participant routes live on the event's own tenant subdomain, so
		// they need the Origin-resolved tenant on top of the PermSelf gate;
		// built here (not threaded from controller.go) since the aggregator
		// already holds both the use case and the apex domain.
		resolveTenant := middleware.ResolveEventTenant(middleware.NewEventTenantResolver(h.useCase), h.cfg.Hosts)
		eventselfHandler.NewEventSelfAPIHandler(h.useCase, h.prot).Init(baseAPI, resolveTenant)

		authHandler.NewAuthAPIHandler(h.useCase, h.prot, h.cfg).Init(baseAPI, baseAPI)
	}
}
