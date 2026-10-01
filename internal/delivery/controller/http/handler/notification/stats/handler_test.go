package stats

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type passThroughProtection struct{}

func (passThroughProtection) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.Next() }
}

type listUseCase struct {
	filter dispatchModel.ListDispatchesFilter
	rows   []dispatchModel.DispatchDetail
}

func (u *listUseCase) ListDispatches(_ context.Context, f dispatchModel.ListDispatchesFilter) ([]dispatchModel.DispatchDetail, int64, error) {
	u.filter = f
	return u.rows, 42, nil
}
func (*listUseCase) GetDispatch(context.Context, uuid.UUID) (*dispatchModel.DispatchDetail, error) {
	panic("not used")
}
func (*listUseCase) GetStats(context.Context, time.Time) (*dispatchModel.Stats, error) {
	panic("not used")
}

func TestListDispatchesCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	uc := &listUseCase{rows: []dispatchModel.DispatchDetail{{DispatchInfo: dispatchModel.DispatchInfo{ID: ids[0]}}, {DispatchInfo: dispatchModel.DispatchInfo{ID: ids[1]}}, {DispatchInfo: dispatchModel.DispatchInfo{ID: ids[2]}}}}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewStatsAPIHandler(uc, passThroughProtection{}).Init(router.Group("/api/notifications"))

	req := httptest.NewRequest(http.MethodGet, "/api/notifications/dispatches?limit=2&status=done&cursor="+ids[0].String(), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if uc.filter.Cursor != ids[0].String() || uc.filter.Status != "done" || uc.filter.Limit != 3 {
		t.Fatalf("filter = %+v", uc.filter)
	}
	var body struct {
		Data struct {
			Items      []struct{ ID uuid.UUID }
			Total      int64
			NextCursor uuid.UUID
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Items) != 2 || body.Data.Total != 42 || body.Data.NextCursor != ids[1] {
		t.Fatalf("page = %+v", body.Data)
	}
}

func TestListDispatchesRejectsInvalidCursor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewStatsAPIHandler(&listUseCase{}, passThroughProtection{}).Init(router.Group("/api/notifications"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/notifications/dispatches?cursor=invalid", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestDetailResponseFillsInAppRecipientAndClassifiesErrors(t *testing.T) {
	d := dispatchModel.DispatchDetail{
		DispatchInfo: dispatchModel.DispatchInfo{RecipientEmail: "ann@example.org", RecipientName: "Ann Lee"},
		Targets: []dispatchModel.DispatchTarget{
			{Channel: "in_app", Status: "done"},
			{Channel: "email", Status: "error", Recipient: "ANN@example.org", Error: "535 5.7.8 bad credentials", FallbackError: "dial tcp 1.2.3.4:587: i/o timeout"},
			{Channel: "email", Status: "error", Recipient: "other@example.org", Error: "Відкладено: вичерпано добовий ліміт"},
		},
	}
	out := toDetailResponse(d)
	if out.RecipientName != "Ann Lee" {
		t.Fatalf("dispatch recipient name = %q", out.RecipientName)
	}
	inApp, email, deferred := out.Targets[0], out.Targets[1], out.Targets[2]
	if inApp.Recipient != "ann@example.org" || inApp.RecipientName != "Ann Lee" {
		t.Fatalf("in-app recipient = %+v", inApp)
	}
	if email.RecipientName != "Ann Lee" || email.ErrorKind != dispatchModel.MailErrorAuth || email.ErrorCode != "535" ||
		email.FallbackErrorKind != dispatchModel.MailErrorConnect {
		t.Fatalf("email target = %+v", email)
	}
	if deferred.RecipientName != "" || deferred.ErrorKind != "" {
		t.Fatalf("a deferral and a foreign address stay as they are: %+v", deferred)
	}
}
