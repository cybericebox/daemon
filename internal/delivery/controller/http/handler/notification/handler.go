package notification

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/catalog"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/inbox"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/pulse"
	notificationSettings "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/settings"
	statsHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/stats"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/template"
	testHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/test"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type (
	// Handler aggregates the notification sub-handlers (inbox, templates, global
	// settings, catalog, test, stats) behind one Init + one use-case interface.
	Handler struct {
		useCase IUseCase
		prot    IProtection
		// site resolves the requesting site (the site query parameter or the Origin) for the pulse,
		// the banners and the inbox.
		site gin.HandlerFunc
	}

	// IUseCase is the union of the notification children that need a use-case
	// (catalog needs none).
	IUseCase interface {
		inbox.IUseCase
		pulse.IUseCase
		template.IUseCase
		notificationSettings.IUseCase
		testHandler.IUseCase
		statsHandler.IUseCase
	}

	// IProtection is the subset of the protection middleware this handler needs.
	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}
)

func NewNotificationsAPIHandler(useCase IUseCase, prot IProtection, site gin.HandlerFunc) *Handler {
	return &Handler{useCase: useCase, prot: prot, site: site}
}

// Init mounts the notifications API under <secured>/notifications.
func (h *Handler) Init(secured *gin.RouterGroup) {
	notifications := secured.Group("notifications")
	inbox.NewInboxAPIHandler(h.useCase, h.prot, h.site).Init(notifications)
	pulse.NewPulseAPIHandler(h.useCase, h.prot, h.site).Init(notifications)
	template.NewTemplateAPIHandler(h.useCase, h.prot).Init(notifications)
	notificationSettings.NewSettingsAPIHandler(h.useCase, h.prot).Init(notifications)
	catalog.NewCatalogAPIHandler(h.prot).Init(notifications)
	testHandler.NewTestAPIHandler(h.useCase, h.prot).Init(notifications)
	statsHandler.NewStatsAPIHandler(h.useCase, h.prot).Init(notifications)
}
