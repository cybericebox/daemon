package notification

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/catalog"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/inbox"
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
	}

	// IUseCase is the union of the notification children that need a use-case
	// (catalog needs none).
	IUseCase interface {
		inbox.IUseCase
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

func NewNotificationsAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

// Init mounts the notifications API under <secured>/notifications.
func (h *Handler) Init(secured *gin.RouterGroup) {
	notifications := secured.Group("notifications")
	inbox.NewInboxAPIHandler(h.useCase, h.prot).Init(notifications)
	template.NewTemplateAPIHandler(h.useCase, h.prot).Init(notifications)
	notificationSettings.NewSettingsAPIHandler(h.useCase, h.prot).Init(notifications)
	catalog.NewCatalogAPIHandler(h.prot).Init(notifications)
	testHandler.NewTestAPIHandler(h.useCase, h.prot).Init(notifications)
	statsHandler.NewStatsAPIHandler(h.useCase, h.prot).Init(notifications)
}
