package stats

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

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	statsUseCase "github.com/cybericebox/daemon/internal/useCase/notification/stats"
)

type passThroughProtection struct{}

func (passThroughProtection) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.Next() }
}

type listUseCase struct {
	filter     dispatchModel.ListDispatchesFilter
	rows       []dispatchModel.DispatchDetail
	resent     []uuid.UUID
	audited    []adminAuditUseCase.Entry
	facetField string
}

func (u *listUseCase) ListDispatches(_ context.Context, f dispatchModel.ListDispatchesFilter) ([]dispatchModel.DispatchDetail, int64, error) {
	u.filter = f
	return u.rows, 42, nil
}
func (*listUseCase) GetDispatch(context.Context, uuid.UUID) (*dispatchModel.DispatchDetail, error) {
	panic("not used")
}
func (u *listUseCase) DispatchFacets(_ context.Context, field string, f dispatchModel.ListDispatchesFilter) (map[string]int64, error) {
	u.filter = f
	u.facetField = field
	if field == "bogus" {
		return nil, statsUseCase.ErrUnknownFacetField
	}
	return map[string]int64{"done": 4}, nil
}
func (*listUseCase) GetStats(context.Context, time.Time) (*dispatchModel.Stats, error) {
	panic("not used")
}

func (u *listUseCase) ResendDispatches(
	_ context.Context,
	ids []uuid.UUID,
) []statsUseCase.ResendResult {
	u.resent = ids
	out := make([]statsUseCase.ResendResult, 0, len(ids))
	for i, id := range ids {
		var failure error
		if i == 1 {
			failure = notificationModel.ErrDispatchNotFailed.Err()
		}
		out = append(out, statsUseCase.ResendResult{DispatchID: id, Err: failure})
	}
	return out
}
func (u *listUseCase) RecordAdminAction(_ context.Context, e adminAuditUseCase.Entry) error {
	u.audited = append(u.audited, e)
	return nil
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

func TestListDispatchesReadsRecipientAndPeriod(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := &listUseCase{}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewStatsAPIHandler(uc, passThroughProtection{}).Init(router.Group("/api/notifications"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(
		http.MethodGet,
		"/api/notifications/dispatches?recipient=%20ann%20&from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z",
		nil,
	))
	if w.Code != http.StatusOK || uc.filter.Recipient != "ann" ||
		uc.filter.From != "2026-10-01T00:00:00Z" ||
		uc.filter.To != "2026-10-02T00:00:00Z" {
		t.Fatalf("status = %d filter = %+v", w.Code, uc.filter)
	}
	w = httptest.NewRecorder()
	router.ServeHTTP(
		w,
		httptest.NewRequest(http.MethodGet, "/api/notifications/dispatches?from=yesterday", nil),
	)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("a malformed period is refused: %d", w.Code)
	}
}

func TestResendDispatchesAnswersPerItemAndAuditsEach(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := &listUseCase{}
	router := gin.New()
	router.Use(response.WithErrorHandler, func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(
			rbac.ContextWithCurrentUserSession(
				ctx.Request.Context(),
				rbac.Claims{UserID: uuid.Must(uuid.NewV7())},
			),
		)
	})
	NewStatsAPIHandler(uc, passThroughProtection{}).Init(router.Group("/api/notifications"))
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	body := `{"DispatchIDs":["` + a.String() + `","` + b.String() + `","` + a.String() + `"]}`
	w := httptest.NewRecorder()
	router.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/notifications/dispatches/resend",
			strings.NewReader(body),
		),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Data struct {
			Results []struct {
				DispatchID uuid.UUID `json:"DispatchID"`
				Ok         bool      `json:"Ok"`
				Code       string    `json:"Code"`
			} `json:"Results"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data.Results) != 3 || !out.Data.Results[0].Ok || out.Data.Results[1].Ok ||
		out.Data.Results[1].Code != "70230" {
		t.Fatalf("results = %+v", out.Data.Results)
	}
	if len(uc.audited) != 3 || uc.audited[1].Target != "dispatch:"+b.String() ||
		uc.audited[1].ResponseStatus != http.StatusConflict {
		t.Fatalf("audit = %+v", uc.audited)
	}

	for _, bad := range []string{`{"DispatchIDs":[]}`, `{}`, `not json`} {
		w = httptest.NewRecorder()
		router.ServeHTTP(
			w,
			httptest.NewRequest(
				http.MethodPost,
				"/api/notifications/dispatches/resend",
				strings.NewReader(bad),
			),
		)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s must be refused: %d", bad, w.Code)
		}
	}
}

func TestListDispatchesPageNumberMode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := &listUseCase{rows: []dispatchModel.DispatchDetail{{DispatchInfo: dispatchModel.DispatchInfo{ID: uuid.Must(uuid.NewV7())}}}}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewStatsAPIHandler(uc, passThroughProtection{}).Init(router.Group("/api/notifications"))
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}

	w := get("/api/notifications/dispatches?page=3&pageSize=20&sortBy=recipient&sortDir=asc&status=done&cursor=" + uuid.Must(uuid.NewV7()).String())
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if uc.filter.Limit != 20 || uc.filter.Offset != 40 || uc.filter.SortBy != "recipient" || uc.filter.SortDir != "asc" ||
		uc.filter.Status != "done" || uc.filter.Cursor != "" {
		t.Fatalf("filter = %+v", uc.filter)
	}
	var body struct {
		Data struct {
			Items    []struct{ ID uuid.UUID } `json:"Items"`
			Total    int64                    `json:"Total"`
			Page     int                      `json:"Page"`
			PageSize int                      `json:"PageSize"`
		} `json:"Data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.Items) != 1 || body.Data.Total != 42 || body.Data.Page != 3 || body.Data.PageSize != 20 {
		t.Fatalf("page = %+v", body.Data)
	}
	for _, bad := range []string{"?page=1&sortBy=password", "?page=1&sortBy=type&sortDir=sideways", "?page=0x", "?page=1&pageSize=99999"} {
		if w = get("/api/notifications/dispatches" + bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s must be refused: %d", bad, w.Code)
		}
	}
}

func TestDispatchFacetsReadTheLogFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := &listUseCase{}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewStatsAPIHandler(uc, passThroughProtection{}).Init(router.Group("/api/notifications"))
	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	w := get("/api/notifications/dispatches/facets?field=status&type=invite&channel=email&recipient=ann&from=2026-10-01T00:00:00Z")
	if w.Code != http.StatusOK || uc.facetField != "status" || uc.filter.Type != "invite" || uc.filter.Channel != "email" ||
		uc.filter.Recipient != "ann" || !strings.Contains(w.Body.String(), `"Counts":{"done":4}`) {
		t.Fatalf("facets: %d %s %+v", w.Code, w.Body.String(), uc.filter)
	}
	for _, bad := range []string{"?field=bogus", "?field=status&from=yesterday"} {
		if w = get("/api/notifications/dispatches/facets" + bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s must be refused: %d", bad, w.Code)
		}
	}
}
