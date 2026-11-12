package adminAudit

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	"github.com/cybericebox/daemon/pkg/tools"
)

type IUseCase interface {
	ListAdminActions(context.Context, adminAuditUseCase.Filter) (adminAuditUseCase.Page, error)
	CountAdminActions(context.Context, adminAuditUseCase.Filter) (int64, error)
	ExportAdminActions(
		context.Context,
		adminAuditUseCase.Filter,
		func(postgres.AdminAuditLog) error,
	) (bool, error)
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
	r.GET("admin/audit-log/export", h.prot.RequirePermission(rbac.PermPlatformAuditRead), h.export)
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
	// Changes are the fields the action changed, before and after; secrets carry Redacted and no value.
	Changes []adminAuditUseCase.Change `json:"Changes"`
}

type auditPage struct {
	Items []auditItem `json:"Items"`
	// Total is how many entries the filters match; set on the first page (no cursor) only.
	Total *int64 `json:"Total"`
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
// @Param    mine        query  bool    false  "true: only the caller's own actions (overrides actorID)"
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
		out.Items = append(out.Items, toItem(x))
	}
	if filter.Cursor == "" {
		total, countErr := h.useCase.CountAdminActions(c.Request.Context(), filter)
		if countErr != nil {
			response.AbortWithError(c, countErr)
			return
		}
		out.Total = &total
	}
	response.AbortWithData(c, out)
}

func toItem(x postgres.AdminAuditLog) auditItem {
	changes := []adminAuditUseCase.Change{}
	if len(x.Changes) > 0 {
		_ = json.Unmarshal(x.Changes, &changes)
	}
	return auditItem{
		x.ID,
		x.ActorID,
		x.Permission,
		x.Method,
		x.Route,
		x.ResponseStatus,
		x.CreatedAt,
		x.Target,
		changes,
	}
}

// export godoc
// @Summary  Admin audit journal as CSV (same filters, no paging)
// @Description Newest first, at most 100000 rows. Each export is itself recorded in the journal.
// @Tags     admin
// @Produce  text/csv
// @Param    actorID     query  string  false  "actor user ID (exact)"
// @Param    mine        query  bool    false  "true: only the caller's own actions (overrides actorID)"
// @Param    permission  query  string  false  "gating permission (exact)"
// @Param    route       query  string  false  "route template contains this text"
// @Param    method      query  string  false  "HTTP method (exact)"
// @Param    status      query  string  false  "final response status: exact (409) or a class (2xx, 4xx, 5xx)"
// @Param    from        query  string  false  "not before (RFC3339)"
// @Param    to          query  string  false  "not after (RFC3339)"
// @Param    targetKind  query  string  false  "kind token of the target"
// @Param    targetID    query  string  false  "an id contained in the target"
// @Success  200  {file}  file
// @Failure  400  {object}  response.Response
// @Router   /admin/audit-log/export [get]
func (h *Handler) export(c *gin.Context) {
	filter, err := parseFilter(c)
	if err != nil {
		response.AbortWithBadRequest(c, err)
		return
	}
	download.ExtendDeadline(c)
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header(
		"Content-Disposition",
		fmt.Sprintf(
			`attachment; filename="audit-%s.csv"`,
			time.Now().UTC().Format("20060102-1504"),
		),
	)
	c.Header("Cache-Control", "private, no-store")
	c.Status(http.StatusOK)
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})
	w := csv.NewWriter(c.Writer)
	_ = w.Write(
		[]string{"Time", "ActorID", "Permission", "Method", "Route", "Status", "Target", "Changes"},
	)
	truncated, err := h.useCase.ExportAdminActions(
		c.Request.Context(),
		filter,
		func(x postgres.AdminAuditLog) error {
			item := toItem(x)
			return w.Write([]string{
				item.CreatedAt.UTC().
					Format(time.RFC3339),
				item.ActorID.String(),
				item.Permission,
				item.Method,
				tools.CSVText(
					item.Route,
				),
				strconv.Itoa(int(item.ResponseStatus)),
				tools.CSVText(item.Target),
				tools.CSVText(changesText(item.Changes)),
			})
		},
	)
	if truncated {
		_ = w.Write(
			[]string{
				"",
				"",
				"",
				"",
				"",
				"",
				"",
				fmt.Sprintf("truncated at %d rows", adminAuditUseCase.MaxExportRows),
			},
		)
	}
	w.Flush()
	if err != nil {
		log.Error().Err(err).Msg("audit export interrupted")
	}
}

// changesText renders the changes of one entry as "Field: before -> after; ...", a redacted field without values.
func changesText(changes []adminAuditUseCase.Change) string {
	parts := make([]string, 0, len(changes))
	for _, ch := range changes {
		if ch.Redacted {
			parts = append(parts, ch.Field+": changed (hidden)")
			continue
		}
		parts = append(
			parts,
			fmt.Sprintf("%s: %v -> %v", ch.Field, show(ch.Before), show(ch.After)),
		)
	}
	return strings.Join(parts, "; ")
}

func show(v any) any {
	if v == nil {
		return "(none)"
	}
	return v
}

func parseFilter(c *gin.Context) (adminAuditUseCase.Filter, error) {
	f, err := parseFilterFields(c)
	if err != nil {
		return f, err
	}
	if c.Query("mine") == "true" {
		claims, ok := rbac.CurrentUserSessionFromContext(c.Request.Context())
		if !ok {
			return f, errors.New("mine needs a signed-in caller")
		}
		f.ActorID = uuid.NullUUID{UUID: claims.UserID, Valid: true}
	}
	return f, nil
}

func parseFilterFields(c *gin.Context) (adminAuditUseCase.Filter, error) {
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
