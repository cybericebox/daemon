package catalog

import (
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// Handler exposes the read-only notification type catalog (types, channels,
// variable descriptors) that drives the admin template editor and test form.
// It reads the in-memory registry directly — no use case needed.
type Handler struct {
	prot IProtection
}

// IProtection is the subset of the protection middleware this handler needs.
type IProtection interface {
	RequirePermission(required rbac.Permission) gin.HandlerFunc
}

type variableResponse struct {
	Name        string `json:"Name"`
	Description string `json:"Description"`
	Default     string `json:"Default"`
}

type typeResponse struct {
	Type      string             `json:"Type"`
	Channels  []string           `json:"Channels"`
	Variables []variableResponse `json:"Variables"`
}

func NewCatalogAPIHandler(prot IProtection) *Handler {
	return &Handler{prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	router.GET("types", h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead), h.listTypes)
	router.GET(
		"types/:type",
		h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
		h.getType,
	)
}

func toResponse(ti notificationTypes.TypeInfo) typeResponse {
	channels := make([]string, 0, len(ti.Channels))
	for _, c := range ti.Channels {
		channels = append(channels, string(c))
	}
	vars := make([]variableResponse, 0)
	for _, d := range notificationTypes.Descriptors(ti.Type) {
		vars = append(
			vars,
			variableResponse{Name: d.Name, Description: d.Description, Default: d.Default},
		)
	}
	return typeResponse{Type: string(ti.Type), Channels: channels, Variables: vars}
}

// listTypes godoc
// @Summary  List all notification types with their channels and template variables
// @Tags     notification-catalog
// @Produce  json
// @Success  200  {object}  response.Response{data=[]typeResponse}
// @Router   /notifications/types [get]
func (h *Handler) listTypes(ctx *gin.Context) {
	infos := notificationTypes.Types()
	out := make([]typeResponse, 0, len(infos))
	for _, ti := range infos {
		out = append(out, toResponse(ti))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	response.AbortWithData(ctx, out)
}

// getType godoc
// @Summary  Get a single notification type with its channels and template variables
// @Tags     notification-catalog
// @Produce  json
// @Param    type  path      string  true  "notification type"
// @Success  200   {object}  response.Response{data=typeResponse}
// @Failure  404   {object}  response.Response
// @Router   /notifications/types/{type} [get]
func (h *Handler) getType(ctx *gin.Context) {
	t := notificationTypes.NotificationType(ctx.Param("type"))
	for _, ti := range notificationTypes.Types() {
		if ti.Type == t {
			response.AbortWithData(ctx, toResponse(ti))
			return
		}
	}
	response.AbortWithNotFound(ctx)
}
