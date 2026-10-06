package adminAudit_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	adminAuditHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/adminAudit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
)

type fakeUC struct {
	got  adminAuditUseCase.Filter
	page adminAuditUseCase.Page
	err  error
}

func (f *fakeUC) ListAdminActions(_ context.Context, filter adminAuditUseCase.Filter) (adminAuditUseCase.Page, error) {
	f.got = filter
	return f.page, f.err
}

type openProt struct{}

func (openProt) RequirePermission(rbac.Permission) gin.HandlerFunc { return func(c *gin.Context) {} }

func serve(uc *fakeUC, query string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	adminAuditHandler.New(uc, openProt{}).Init(r.Group("api"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/audit-log"+query, nil))
	return w
}

func TestAuditLogParsesEveryFilter(t *testing.T) {
	uc := &fakeUC{}
	actor := uuid.Must(uuid.NewV7())
	w := serve(uc, "?actorID="+actor.String()+"&permission=users.delete&route=/users&method=delete&status=4xx&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00%2B03:00&targetKind=event&targetID=abc&cursor=c&limit=25")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	f := uc.got
	if f.ActorID.UUID != actor || f.Permission != "users.delete" || f.Route != "/users" || f.Method != "DELETE" ||
		f.StatusMin != 400 || f.StatusMax != 499 || f.TargetKind != "event" || f.TargetID != "abc" || f.Cursor != "c" || f.Limit != 25 {
		t.Fatalf("filter = %+v", f)
	}
	if f.From == nil || f.To == nil || !f.From.Equal(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("from/to = %v %v", f.From, f.To)
	}
}

func TestAuditLogExactStatusAndClasses(t *testing.T) {
	for query, want := range map[string][2]int32{"409": {409, 409}, "2XX": {200, 299}, "5xx": {500, 599}} {
		uc := &fakeUC{}
		if w := serve(uc, "?status="+query); w.Code != http.StatusOK || uc.got.StatusMin != want[0] || uc.got.StatusMax != want[1] {
			t.Errorf("status=%s: %d %+v", query, w.Code, uc.got)
		}
	}
}

func TestAuditLogRefusesBadInput(t *testing.T) {
	for _, query := range []string{
		"?actorID=nope", "?status=abc", "?status=99", "?status=7xx", "?method=DROP%20TABLE", "?from=yesterday", "?to=2026-13-01",
		"?limit=0", "?limit=201", "?limit=x", "?from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z",
	} {
		if w := serve(&fakeUC{}, query); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", query, w.Code)
		}
	}
	if w := serve(&fakeUC{err: adminAuditUseCase.ErrInvalidCursor}, "?cursor=garbage"); w.Code != http.StatusBadRequest {
		t.Errorf("a bad cursor is a client error: %d", w.Code)
	}
}

func TestAuditLogResponseShape(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	uc := &fakeUC{page: adminAuditUseCase.Page{
		Items:      []postgres.AdminAuditLog{{ID: id, Permission: "users.delete", Method: "DELETE", Route: "/users/:userID", ResponseStatus: 409, Target: "userID:x"}},
		NextCursor: "next",
	}}
	w := serve(uc, "")
	var env struct {
		Data struct {
			Items      []map[string]any
			NextCursor string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || len(env.Data.Items) != 1 || env.Data.NextCursor != "next" {
		t.Fatalf("body %s (%v)", w.Body.String(), err)
	}
	for _, key := range []string{"ID", "ActorID", "Permission", "Method", "Route", "ResponseStatus", "CreatedAt", "Target"} {
		if _, ok := env.Data.Items[0][key]; !ok {
			t.Errorf("item lacks %s", key)
		}
	}
	if w := serve(&fakeUC{}, ""); !json.Valid(w.Body.Bytes()) || !strings.Contains(w.Body.String(), `"Items":[]`) {
		t.Errorf("an empty journal is an empty list, not null: %s", w.Body.String())
	}
}
