package adminAudit

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"time"
)

type IUseCase interface {
	ListAdminActions(context.Context, uuid.NullUUID, string, string, int32) ([]postgres.AdminAuditLog, error)
}
type IProtection interface {
	RequirePermission(rbac.Permission) gin.HandlerFunc
}
type Handler struct {
	useCase IUseCase
	prot    IProtection
}

func New(useCase IUseCase, prot IProtection) *Handler { return &Handler{useCase, prot} }
func (h *Handler) Init(r *gin.RouterGroup) {
	r.GET("admin/audit-log", h.prot.RequirePermission(rbac.PermPlatformAuditRead), h.list)
}
func (h *Handler) list(c *gin.Context) {
	actor := uuid.NullUUID{}
	if raw := c.Query("actorID"); raw != "" {
		id, err := uuid.FromString(raw)
		if err != nil {
			response.AbortWithBadRequest(c, err)
			return
		}
		actor = uuid.NullUUID{UUID: id, Valid: true}
	}
	rows, err := h.useCase.ListAdminActions(c, actor, c.Query("permission"), c.Query("route"), 50)
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	type item struct {
		ID, ActorID               uuid.UUID
		Permission, Method, Route string
		ResponseStatus            int32
		CreatedAt                 time.Time
		Target                    string
	}
	out := make([]item, 0, len(rows))
	for _, x := range rows {
		out = append(out, item{x.ID, x.ActorID, x.Permission, x.Method, x.Route, x.ResponseStatus, x.CreatedAt, x.Target})
	}
	response.AbortWithData(c, out)
}
