package user_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	userHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/user"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

// fakeUC satisfies IUseCase.
type fakeUC struct {
	users         []authUseCase.UserInfo
	hasMore       bool
	nextCursor    uuid.UUID
	stats         authUseCase.UserStats
	updateErr     error
	deleteErr     error
	inviteErr     error
	inviteResults []authUseCase.InviteResult
	roleCaller    uuid.UUID
	statusCaller  uuid.UUID
	deleteCaller  uuid.UUID
	inviteCaller  uuid.UUID
	bulkResults   []authUseCase.BulkUserResult
	bulkAction    string
	facetField    string
	facetFilter   authUseCase.UsersFilter
	audited       []adminAuditUseCase.Entry
	listFilter    authUseCase.UsersFilter
	entries       []authUseCase.InviteEntry
	entryResults  []authUseCase.InviteEntryResult
}

func (f *fakeUC) ListUsers(
	_ context.Context,
	filter authUseCase.UsersFilter,
) (authUseCase.UsersListResult, error) {
	f.listFilter = filter
	return authUseCase.UsersListResult{
		Users:      f.users,
		NextCursor: f.nextCursor,
		HasMore:    f.hasMore,
	}, nil
}

func TestListUsers_OffsetPageContract(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/users?page=3&pageSize=25&sortBy=name&sortDir=asc&status=active", nil))
	if w.Code != http.StatusOK || uc.listFilter.Page != 3 || uc.listFilter.PageSize != 25 ||
		uc.listFilter.SortBy != "name" || uc.listFilter.SortDir != "asc" || uc.listFilter.Status != userModel.UserStatusActive {
		t.Fatalf("offset list: status=%d filter=%+v body=%s", w.Code, uc.listFilter, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"Page":3`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"Total":0`)) {
		t.Fatalf("offset envelope: %s", w.Body.String())
	}
}
func (f *fakeUC) GetUserStats(_ context.Context) (authUseCase.UserStats, error) {
	return f.stats, nil
}
func (f *fakeUC) GetUser(_ context.Context, _ uuid.UUID) (*authUseCase.UserDetail, error) {
	return &authUseCase.UserDetail{}, nil
}
func (f *fakeUC) UpdateUserRole(ctx context.Context, _ uuid.UUID, _ rbac.Role) error {
	if caller, ok := rbac.CurrentUserSessionFromContext(ctx); ok {
		f.roleCaller = caller.UserID
	}
	return f.updateErr
}
func (f *fakeUC) UpdateUserStatus(ctx context.Context, _ uuid.UUID, _ userModel.UserStatus) error {
	if caller, ok := rbac.CurrentUserSessionFromContext(ctx); ok {
		f.statusCaller = caller.UserID
	}
	return f.updateErr
}
func (f *fakeUC) DeleteUser(ctx context.Context, _ uuid.UUID) error {
	if caller, ok := rbac.CurrentUserSessionFromContext(ctx); ok {
		f.deleteCaller = caller.UserID
	}
	return f.deleteErr
}
func (f *fakeUC) InviteUser(ctx context.Context, _ string, _ rbac.Role, _, _ string) error {
	if caller, ok := rbac.CurrentUserSessionFromContext(ctx); ok {
		f.inviteCaller = caller.UserID
	}
	return f.inviteErr
}

func (f *fakeUC) InviteUsers(
	ctx context.Context,
	_ rbac.Role,
	_ []string,
) ([]authUseCase.InviteResult, error) {
	if caller, ok := rbac.CurrentUserSessionFromContext(ctx); ok {
		f.inviteCaller = caller.UserID
	}
	return f.inviteResults, f.inviteErr
}

func (f *fakeUC) InviteEntries(_ context.Context, entries []authUseCase.InviteEntry) ([]authUseCase.InviteEntryResult, error) {
	f.entries = entries
	return f.entryResults, f.inviteErr
}

// fakeProt satisfies IProtection; RequirePermission is a no-op pass-through.
type fakeProt struct{}

func (fakeProt) RequirePermission(_ rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

// injectIdentity simulates RequireAuthentication for secured routes.
func injectIdentity(uid uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Inject super_admin role so use-case permission checks pass.
		rc := rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uid, Role: rbac.RoleSuperAdmin})
		c.Request = c.Request.WithContext(rc)
		c.Next()
	}
}

// newEngine builds a test router wired with the user handler and error middleware.
func newEngine(uc *fakeUC, uid uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.Use(injectIdentity(uid))
	api := r.Group("api")
	userHandler.NewUserAPIHandler(uc, fakeProt{}).Init(api)
	return r
}

func TestListUsers_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	id1 := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		users: []authUseCase.UserInfo{
			{
				ID:        id1,
				FirstName: "Alice",
				LastName:  "Smith",
				Email:     "alice@test.test",
				Role:      "user",
			},
		},
		hasMore: false,
	}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/users", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Items []struct {
				Email string
			}
			NextCursor *string
			Total      int64
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Data.Items) != 1 || env.Data.Items[0].Email != "alice@test.test" {
		t.Fatalf("unexpected users: %+v", env.Data.Items)
	}
	if env.Data.NextCursor != nil {
		t.Fatalf("NextCursor = %v, want omitted (no further page)", *env.Data.NextCursor)
	}
}

func TestUserStats_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		stats: authUseCase.UserStats{
			Total:   7,
			Blocked: 2,
			ByRole:  []authUseCase.RoleCount{{Role: "admin", Count: 1}},
		},
	}
	r := newEngine(uc, uid)
	req := httptest.NewRequest(http.MethodGet, "/api/users/stats", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateRole_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	targetID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uid)

	body, _ := json.Marshal(map[string]string{"Role": "admin"})
	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/users/"+targetID.String()+"/role",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if uc.roleCaller != uid {
		t.Fatalf("role update must receive the authenticated request context, got %s", uc.roleCaller)
	}
}

func TestUpdateRole_ErrCannotAssignRole_ReturnsForbidden(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	targetID := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{updateErr: authModel.ErrCannotAssignRole.Err()}, uid)

	body, _ := json.Marshal(map[string]string{"Role": "super_admin"})
	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/users/"+targetID.String()+"/role",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateStatus_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	targetID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uid)

	body, _ := json.Marshal(map[string]string{"Status": "blocked"})
	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/users/"+targetID.String()+"/status",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if uc.statusCaller != uid {
		t.Fatalf("status update must receive the authenticated request context, got %s", uc.statusCaller)
	}
}

func TestDeleteUser_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	targetID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/"+targetID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if uc.deleteCaller != uid {
		t.Fatalf("delete must receive the authenticated request context, got %s", uc.deleteCaller)
	}
}

func TestBadUserID_Returns400(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{}, uid)

	req := httptest.NewRequest(http.MethodDelete, "/api/users/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// AbortWithBadRequest uses ErrInvalidData which has an application-level
	// bad-request status but still returns HTTP 200 with a non-success code
	// in the body via AbortWithStatus. Check that it is NOT 200 success (OK)
	// by asserting the body contains a non-success status code, or check HTTP.
	// AbortWithBadRequest calls AbortWithStatus which calls AbortWithStatusJSON
	// with HTTPCode from the err package — ErrInvalidData maps to 400.
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestInviteSingle_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uid)

	body, _ := json.Marshal(map[string]string{
		"Email":     "invite@test.test",
		"Role":      "user",
		"FirstName": "Bob",
		"LastName":  "Builder",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/users/invite", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if uc.inviteCaller != uid {
		t.Fatalf("invite must receive the authenticated request context, got %s", uc.inviteCaller)
	}
}

func TestInviteBulk_Returns200WithResults(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		inviteResults: []authUseCase.InviteResult{
			{Email: "a@test.test", Err: nil},
			{Email: "b@test.test", Err: nil},
		},
	}
	r := newEngine(uc, uid)

	body, _ := json.Marshal(map[string]interface{}{
		"Emails": []string{"a@test.test", "b@test.test"},
		"Role":   "user",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/users/invite", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if uc.inviteCaller != uid {
		t.Fatalf("bulk invite must receive the authenticated request context, got %s", uc.inviteCaller)
	}
	var env struct {
		Data []struct {
			Email string
			Error string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Data) != 2 {
		t.Fatalf("want 2 results, got %d: %+v", len(env.Data), env.Data)
	}
	if env.Data[0].Email != "a@test.test" {
		t.Fatalf("want a@test.test, got %s", env.Data[0].Email)
	}
}

func TestInviteEntries_PassesPerPersonRolesAndReturnsCodes(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{entryResults: []authUseCase.InviteEntryResult{
		{Email: "a@test.test", Role: rbac.RoleUser},
		{Email: "b@test.test", Role: rbac.RoleSuperAdmin, Code: authUseCase.InviteCodeRoleForbidden},
	}}
	r := newEngine(uc, uid)
	body, _ := json.Marshal(map[string]any{"Entries": []map[string]string{
		{"Email": "a@test.test", "FirstName": "Ann"},
		{"Email": "b@test.test", "Role": "super_admin"},
	}})
	req := httptest.NewRequest(http.MethodPost, "/api/users/invite", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if len(uc.entries) != 2 || uc.entries[0].FirstName != "Ann" || uc.entries[0].Role != "" || uc.entries[1].Role != rbac.RoleSuperAdmin {
		t.Fatalf("unexpected entries: %+v", uc.entries)
	}
	var out struct {
		Data []struct{ Email, Role, Code string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Data) != 2 || out.Data[1].Code != "role_forbidden" || out.Data[0].Code != "" || out.Data[0].Role != "user" {
		t.Fatalf("unexpected response: %s", w.Body.String())
	}
}

func TestListUsersIncludesEmailConfirmationForRecipientSearch(t *testing.T) {
	for _, url := range []string{"/api/users?search=operator&pageSize=10", "/api/users?page=1&pageSize=10"} {
		t.Run(url, func(t *testing.T) {
			uc := &fakeUC{users: []authUseCase.UserInfo{{ID: uuid.Must(uuid.NewV7()), Email: "operator@example.test", EmailConfirmed: true}}}
			w := httptest.NewRecorder()
			newEngine(uc, uuid.Must(uuid.NewV7())).ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
			var env struct {
				Data struct{ Items []map[string]any }
			}
			if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
				t.Fatal(err)
			}
			if len(env.Data.Items) != 1 || env.Data.Items[0]["EmailConfirmed"] != true {
				t.Fatalf("missing confirmation metadata: %s", w.Body.String())
			}
		})
	}
}

func (f *fakeUC) ListUserParticipations(
	context.Context,
	uuid.UUID,
) ([]authUseCase.UserParticipationView, error) {
	return []authUseCase.UserParticipationView{{EventName: "CTF", Role: "captain"}}, nil
}
func (f *fakeUC) EndUserSessions(context.Context, uuid.UUID) error      { return f.updateErr }
func (f *fakeUC) ResendUserInvitation(context.Context, uuid.UUID) error { return f.updateErr }

func (f *fakeUC) BulkUserAction(
	_ context.Context,
	action string,
	_ []uuid.UUID,
) ([]authUseCase.BulkUserResult, error) {
	f.bulkAction = action
	return f.bulkResults, nil
}
func (f *fakeUC) RecordAdminAction(_ context.Context, entry adminAuditUseCase.Entry) error {
	f.audited = append(f.audited, entry)
	return nil
}

func TestBulkReportsEachUserAndAuditsEachOne(t *testing.T) {
	uid, ok1, bad := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		bulkResults: []authUseCase.BulkUserResult{
			{UserID: ok1},
			{UserID: bad, Err: authModel.ErrInsufficientPermission.Err()},
		},
	}
	r := newEngine(uc, uid)

	body := `{"UserIDs":["` + ok1.String() + `","` + bad.String() + `"]}`
	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(http.MethodPost, "/api/users/bulk/block", strings.NewReader(body)),
	)

	if w.Code != http.StatusOK || uc.bulkAction != authUseCase.BulkBlock {
		t.Fatalf("status = %d action = %q body = %s", w.Code, uc.bulkAction, w.Body.String())
	}
	var out struct {
		Data struct {
			Results []struct {
				UserID uuid.UUID
				Ok     bool
				Code   string
			}
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	res := out.Data.Results
	if len(res) != 2 || !res[0].Ok || res[1].Ok || res[1].Code == "" || res[0].UserID != ok1 {
		t.Fatalf("results = %+v", res)
	}
	if len(uc.audited) != 2 || uc.audited[0].Target != "user:"+ok1.String() ||
		uc.audited[1].Target != "user:"+bad.String() ||
		uc.audited[0].ResponseStatus != http.StatusOK ||
		uc.audited[1].ResponseStatus != http.StatusForbidden ||
		uc.audited[0].ActorID != uid {
		t.Fatalf("audit = %+v", uc.audited)
	}
}

func TestListUsersRejectsUnknownFilters(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{}, uid)
	for _, query := range []string{"signIn=sms", "emailConfirmed=maybe", "inactiveDays=0", "inactiveDays=abc"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(
			w,
			httptest.NewRequest(http.MethodGet, "/api/users?page=1&pageSize=10&"+query, nil),
		)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", query, w.Code)
		}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodGet,
			"/api/users?page=1&pageSize=10&signIn=google&emailConfirmed=true&inactiveDays=30",
			nil,
		),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("valid filters: status = %d body = %s", w.Code, w.Body.String())
	}
}

func (f *fakeUC) UserFacets(_ context.Context, field string, filter authUseCase.UsersFilter) (map[string]int64, error) {
	f.facetField, f.facetFilter = field, filter
	return map[string]int64{"password": 3}, nil
}

func TestUserFacetsReadTheFieldAndTheOtherFilters(t *testing.T) {
	uc := &fakeUC{}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/users/facets?field=signIn&role=user&status=active&emailConfirmed=true&inactiveDays=30", nil))

	if w.Code != http.StatusOK || uc.facetField != "signIn" || uc.facetFilter.Status != "active" || uc.facetFilter.EmailConfirmed != "true" ||
		uc.facetFilter.InactiveDays != 30 || len(uc.facetFilter.Roles) != 1 || !strings.Contains(w.Body.String(), `"password":3`) {
		t.Fatalf("status = %d field = %q filter = %+v body = %s", w.Code, uc.facetField, uc.facetFilter, w.Body.String())
	}
	for _, target := range []string{"/api/users/facets", "/api/users/facets?field=picture", "/api/users/facets?field=role&signIn=sms"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d", target, w.Code)
		}
	}
}
