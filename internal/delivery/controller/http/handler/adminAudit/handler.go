package adminAudit

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
)

type IUseCase interface {
	ListAdminActions(context.Context, adminAuditUseCase.Filter) (adminAuditUseCase.Page, error)
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

type auditItem struct {
	ID             uuid.UUID `json:"ID"`
	ActorID        uuid.UUID `json:"ActorID"`
	Permission     string    `json:"Permission"`
	Method         string    `json:"Method"`
	Route          string    `json:"Route"`
	ResponseStatus int32     `json:"ResponseStatus"`
	CreatedAt      time.Time `json:"CreatedAt"`
	Target         string    `json:"Target"`
}

type auditPage struct {
	Items []auditItem `json:"Items"`
	// NextCursor continues the list after the last item; empty on the last page.
	NextCursor string `json:"NextCursor"`
}

var (
	statusExact = regexp.MustCompile(`^[1-5][0-9]{2}$`)
	statusClass = regexp.MustCompile(`^[1-5]xx$`)
	methodName  = regexp.MustCompile(`^[A-Za-z]{3,7}$`)
)

// list godoc
// @Summary  Admin audit journal (filtered, keyset-paged, newest first)
// @Tags     admin
// @Produce  json
// @Param    actorID     query  string  false  "actor user ID (exact)"
// @Param    permission  query  string  false  "gating permission (exact), e.g. users.delete"
// @Param    route       query  string  false  "route template contains this text"
// @Param    method      query  string  false  "HTTP method (exact): POST PUT PATCH DELETE, GET for exports"
// @Param    status      query  string  false  "final response status: exact (409) or a class (2xx, 4xx, 5xx)"
// @Param    from        query  string  false  "not before (RFC3339)"
// @Param    to          query  string  false  "not after (RFC3339)"
// @Param    targetKind  query  string  false  "kind token of the target: event, team, agent, test-lab, exercise, userID, id..."
// @Param    targetID    query  string  false  "an id contained in the target"
// @Param    cursor      query  string  false  "NextCursor of the previous page"
// @Param    limit       query  int     false  "page size 1-200 (default 50)"
// @Success  200  {object}  response.Response{data=auditPage}
// @Failure  400  {object}  response.Response
// @Router   /admin/audit-log [get]
func (h *Handler) list(c *gin.Context) {
	filter, err := parseFilter(c)
	if err != nil {
		response.AbortWithBadRequest(c, err)
		return
	}
	page, err := h.useCase.ListAdminActions(c.Request.Context(), filter)
	if err != nil {
		if errors.Is(err, adminAuditUseCase.ErrInvalidCursor) {
			response.AbortWithBadRequest(c, err)
			return
		}
		response.AbortWithError(c, err)
		return
	}
	out := auditPage{Items: make([]auditItem, 0, len(page.Items)), NextCursor: page.NextCursor}
	for _, x := range page.Items {
		out.Items = append(out.Items, auditItem{x.ID, x.ActorID, x.Permission, x.Method, x.Route, x.ResponseStatus, x.CreatedAt, x.Target})
	}
	response.AbortWithData(c, out)
}

func parseFilter(c *gin.Context) (adminAuditUseCase.Filter, error) {
	f := adminAuditUseCase.Filter{
		Permission: c.Query("permission"), Route: c.Query("route"),
		TargetKind: c.Query("targetKind"), TargetID: c.Query("targetID"), Cursor: c.Query("cursor"),
	}
	if raw := c.Query("actorID"); raw != "" {
		id, err := uuid.FromString(raw)
		if err != nil {
			return f, fmt.Errorf("actorID is not a UUID")
		}
		f.ActorID = uuid.NullUUID{UUID: id, Valid: true}
	}
	if raw := c.Query("method"); raw != "" {
		if !methodName.MatchString(raw) {
			return f, fmt.Errorf("method is not an HTTP method")
		}
		f.Method = strings.ToUpper(raw)
	}
	if raw := strings.ToLower(c.Query("status")); raw != "" {
		switch {
		case statusExact.MatchString(raw):
			n, _ := strconv.Atoi(raw)
			f.StatusMin, f.StatusMax = int32(n), int32(n)
		case statusClass.MatchString(raw):
			n := int32(raw[0]-'0') * 100
			f.StatusMin, f.StatusMax = n, n+99
		default:
			return f, fmt.Errorf("status must be a code (409) or a class (4xx)")
		}
	}
	for name, dst := range map[string]**time.Time{"from": &f.From, "to": &f.To} {
		if raw := c.Query(name); raw != "" {
			at, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				return f, fmt.Errorf("%s must be RFC3339", name)
			}
			*dst = &at
		}
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return f, fmt.Errorf("to is before from")
	}
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > adminAuditUseCase.MaxPageSize {
			return f, fmt.Errorf("limit must be between 1 and %d", adminAuditUseCase.MaxPageSize)
		}
		f.Limit = int32(n)
	}
	return f, nil
}
