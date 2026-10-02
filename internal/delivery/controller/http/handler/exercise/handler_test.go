package exercise_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	exerciseHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/exercise"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

// fakeUC satisfies exerciseHandler.IUseCase.
type fakeUC struct {
	deviceCall            string
	flagPolicy            exerciseUseCase.FlagPolicy
	exerciseView          exerciseUseCase.ExerciseView
	listResult            exerciseUseCase.ExercisesListResult
	listFilter            exerciseUseCase.ExercisesFilter
	tagPrefix             string
	tagLimit              int
	accessInput           exerciseUseCase.SetAccessInput
	tagSuggestions        []exerciseUseCase.ExerciseTagSuggestion
	versionView           exerciseUseCase.VersionView
	versionList           []exerciseUseCase.VersionListItem
	usage                 exerciseUseCase.ExerciseUsage
	checkpointCalled      bool
	sessionLink           labaccess.Link
	checkpointLabel       string
	restoredVersionID     uuid.UUID
	laboratoriesAvailable bool
	testDeploys           []exerciseModel.TestDeploy
	listDeploysExercise   uuid.UUID
	checked               string

	err error

	// capture what the handler actually sent, so SaveDraft's "don't reuse the
	// request DTO afterwards" contract can be asserted.
	savedVariants []exerciseModel.Variant

	// files
	uploadedFile     mediaModel.File
	streamedFile     mediaModel.File
	streamedContent  string
	uploadName       string
	uploadContent    string
	uploadContentTyp string
	uploadCreatedBy  uuid.UUID
	streamedFileID   uuid.UUID
	maxUploadBytes   int64

	// deploy
	deployHandle exerciseModel.DeployHandle
	deployStatus exerciseModel.LabDeployStatus

	// archive
	exportedArchive []byte

	// lifecycle: archive/unarchive
	archivedID   uuid.UUID
	unarchivedID uuid.UUID
}

func (f *fakeUC) CreateExercise(_ context.Context, _ exerciseUseCase.CreateExerciseInput) (exerciseUseCase.ExerciseView, error) {
	return f.exerciseView, f.err
}
func (f *fakeUC) GetExercise(_ context.Context, _ uuid.UUID) (exerciseUseCase.ExerciseView, error) {
	return f.exerciseView, f.err
}
func (f *fakeUC) ListExercises(_ context.Context, filter exerciseUseCase.ExercisesFilter) (exerciseUseCase.ExercisesListResult, error) {
	f.listFilter = filter
	return f.listResult, f.err
}
func (f *fakeUC) ListExerciseTags(_ context.Context, _ exerciseUseCase.Actor, prefix string, limit int) ([]exerciseUseCase.ExerciseTagSuggestion, error) {
	f.tagPrefix, f.tagLimit = prefix, limit
	return f.tagSuggestions, f.err
}
func (f *fakeUC) UpdateExerciseIdentity(_ context.Context, _ exerciseUseCase.UpdateExerciseInput) (exerciseUseCase.ExerciseView, error) {
	return f.exerciseView, f.err
}
func (f *fakeUC) ArchiveExercise(_ context.Context, id, _ uuid.UUID) (exerciseUseCase.ExerciseView, error) {
	f.archivedID = id
	return f.exerciseView, f.err
}
func (f *fakeUC) UnarchiveExercise(_ context.Context, id, _ uuid.UUID) (exerciseUseCase.ExerciseView, error) {
	f.unarchivedID = id
	return f.exerciseView, f.err
}
func (f *fakeUC) GetExerciseUsage(_ context.Context, _ uuid.UUID) (exerciseUseCase.ExerciseUsage, error) {
	return f.usage, f.err
}
func (f *fakeUC) DeleteExercise(_ context.Context, _ uuid.UUID) error {
	return f.err
}
func (f *fakeUC) SaveDraft(_ context.Context, _ uuid.UUID, in exerciseUseCase.SaveDraftInput) (exerciseUseCase.VersionView, error) {
	f.savedVariants = in.Variants
	return f.versionView, f.err
}
func (f *fakeUC) PublishDraft(_ context.Context, _ uuid.UUID) (exerciseUseCase.VersionView, error) {
	return f.versionView, f.err
}
func (f *fakeUC) DiscardDraft(_ context.Context, _ uuid.UUID) error {
	return f.err
}
func (f *fakeUC) RollbackToVersion(_ context.Context, _, _, _ uuid.UUID) (exerciseUseCase.VersionView, error) {
	return f.versionView, f.err
}
func (f *fakeUC) CreateCheckpoint(_ context.Context, _, _ uuid.UUID, label string) (exerciseUseCase.VersionView, error) {
	f.checkpointCalled = true
	f.checkpointLabel = label
	return f.versionView, f.err
}
func (f *fakeUC) RestoreToVersion(_ context.Context, _, versionID, _ uuid.UUID) (exerciseUseCase.VersionView, error) {
	f.restoredVersionID = versionID
	return f.versionView, f.err
}
func (f *fakeUC) GetVersion(_ context.Context, _, _ uuid.UUID) (exerciseUseCase.VersionView, error) {
	return f.versionView, f.err
}
func (f *fakeUC) GetWorkingCopy(_ context.Context, _ uuid.UUID) (exerciseUseCase.VersionView, error) {
	return f.versionView, f.err
}
func (f *fakeUC) ListVersions(_ context.Context, _ uuid.UUID) ([]exerciseUseCase.VersionListItem, error) {
	return f.versionList, f.err
}
func (f *fakeUC) UploadFile(_ context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID) (mediaModel.File, error) {
	f.uploadName = name
	f.uploadContentTyp = contentType
	f.uploadCreatedBy = createdBy
	b, _ := io.ReadAll(r)
	f.uploadContent = string(b)
	return f.uploadedFile, f.err
}
func (f *fakeUC) StreamFile(_ context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	f.streamedFileID = id
	if f.err != nil {
		return nil, mediaModel.File{}, f.err
	}
	return io.NopCloser(bytes.NewBufferString(f.streamedContent)), f.streamedFile, nil
}
func (f *fakeUC) MaxUploadBytes() int64          { return f.maxUploadBytes }
func (f *fakeUC) InfrastructureAvailable() bool  { return f.laboratoriesAvailable }
func (f *fakeUC) MaxActiveTestDeploys() int      { return 2 }
func (f *fakeUC) DevicePersistenceAllowed() bool { return true }
func (f *fakeUC) DeviceLimits() (infraModel.LimitsFeature, bool) {
	return infraModel.LimitsFeature{}, false
}
func (f *fakeUC) FlagPolicy() exerciseUseCase.FlagPolicy { return f.flagPolicy }
func (f *fakeUC) DeployVariantTest(_ context.Context, _, _, _ uuid.UUID) (exerciseModel.DeployHandle, error) {
	return f.deployHandle, f.err
}
func (f *fakeUC) DeployTestStatus(_ context.Context, _, _ uuid.UUID) (exerciseModel.LabDeployStatus, error) {
	return f.deployStatus, f.err
}
func (f *fakeUC) DestroyDeployTest(_ context.Context, _, _ uuid.UUID) error { return f.err }
func (f *fakeUC) ResetTestDeployDevice(_ context.Context, userID, _ uuid.UUID, device string) error {
	f.deviceCall = "reset " + device + " by " + userID.String()
	return f.err
}
func (f *fakeUC) RescueTestDeployDevice(_ context.Context, _, _ uuid.UUID, device string, enable bool) error {
	f.deviceCall = "rescue " + device + " " + strconv.FormatBool(enable)
	return f.err
}
func (f *fakeUC) ListTestDeploys(_ context.Context, _, exerciseID uuid.UUID) ([]exerciseModel.TestDeploy, error) {
	f.listDeploysExercise = exerciseID
	return f.testDeploys, f.err
}
func (f *fakeUC) ExtendTestDeploy(_ context.Context, _, _ uuid.UUID) (exerciseModel.TestDeploy, error) {
	return exerciseModel.TestDeploy{}, f.err
}
func (f *fakeUC) CheckTestFlag(_ context.Context, _, _, taskID uuid.UUID, flag string) (bool, error) {
	f.checked = flag
	return flag == "ICE{ok}" && taskID != uuid.Nil, f.err
}
func (f *fakeUC) OpenTestDeployLink(_ context.Context, _, _ uuid.UUID, _ string, _ int32) (labaccess.Link, error) {
	return f.sessionLink, f.err
}
func (f *fakeUC) ExportExerciseArchive(_ context.Context, _ uuid.UUID, _ exerciseUseCase.ExportOptions) ([]byte, error) {
	return f.exportedArchive, f.err
}
func (f *fakeUC) ImportExerciseArchive(_ context.Context, _ exerciseUseCase.ImportExerciseInput) (exerciseUseCase.ExerciseView, error) {
	return f.exerciseView, f.err
}

// fakeProt satisfies exerciseHandler.IProtection; RequirePermission is a no-op pass-through.
type fakeProt struct{}

func (fakeProt) RequirePermission(_ rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

type roleProt struct{ role rbac.Role }

func (p roleProt) RequirePermission(permission rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !p.role.HasPermission(permission) {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		// Like the real gate: the caller's claims travel to the policy.
		claims, ok := rbac.CurrentUserSessionFromContext(c.Request.Context())
		if !ok {
			claims = rbac.Claims{UserID: uuid.Must(uuid.NewV7())}
		}
		claims.Role = p.role
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), claims))
		c.Next()
	}
}

// injectIdentity simulates the authenticated caller RequirePermission would
// have already let through.
func injectIdentity(uid uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		rc := rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uid, Role: rbac.RoleSuperAdmin})
		c.Request = c.Request.WithContext(rc)
		c.Next()
	}
}

func newEngine(uc *fakeUC, uid uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.Use(injectIdentity(uid))
	api := r.Group("api")
	exerciseHandler.NewExerciseAPIHandler(uc, fakeProt{}).Init(api)
	return r
}

func TestExerciseCheckpointAndRestoreRoutes(t *testing.T) {
	uid, exID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{versionView: exerciseUseCase.VersionView{ID: versionID, ExerciseID: exID, Status: "checkpoint"}}
	r := newEngine(uc, uid)
	checkpoint := httptest.NewRecorder()
	r.ServeHTTP(checkpoint, httptest.NewRequest(http.MethodPost, "/api/exercises/"+exID.String()+"/checkpoints", nil))
	if checkpoint.Code != http.StatusOK || !uc.checkpointCalled {
		t.Fatalf("checkpoint route: status=%d called=%t body=%s", checkpoint.Code, uc.checkpointCalled, checkpoint.Body.String())
	}
	restore := httptest.NewRecorder()
	r.ServeHTTP(restore, httptest.NewRequest(http.MethodPost, "/api/exercises/"+exID.String()+"/versions/"+versionID.String()+"/restore", nil))
	if restore.Code != http.StatusOK || uc.restoredVersionID != versionID {
		t.Fatalf("restore route: status=%d source=%s body=%s", restore.Code, uc.restoredVersionID, restore.Body.String())
	}
}

func TestCheckpoint_OptionalNoteBecomesLabel(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		name      string
		body      io.Reader
		want      int
		wantLabel string
	}{
		{"no body", nil, http.StatusOK, ""},
		{"empty object", strings.NewReader(`{}`), http.StatusOK, ""},
		{"note", strings.NewReader(`{"Note":"Before refactor"}`), http.StatusOK, "Before refactor"},
		{"500 cyrillic runes", strings.NewReader(`{"Note":"` + strings.Repeat("я", 500) + `"}`), http.StatusOK, strings.Repeat("я", 500)},
		{"501 runes", strings.NewReader(`{"Note":"` + strings.Repeat("я", 501) + `"}`), http.StatusBadRequest, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			uc := &fakeUC{versionView: exerciseUseCase.VersionView{ExerciseID: exID, Status: "checkpoint", Label: tc.wantLabel}}
			r := newEngine(uc, uuid.Must(uuid.NewV7()))
			req := httptest.NewRequest(http.MethodPost, "/api/exercises/"+exID.String()+"/checkpoints", tc.body)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want || uc.checkpointLabel != tc.wantLabel {
				t.Fatalf("code=%d label=%q body=%s", w.Code, uc.checkpointLabel, w.Body.String())
			}
			if tc.want == http.StatusOK && !strings.Contains(w.Body.String(), `"Label":"`+tc.wantLabel+`"`) {
				t.Fatalf("version response must carry Label: %s", w.Body.String())
			}
		})
	}
}

func TestListVersions_SerializesLabel(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{versionList: []exerciseUseCase.VersionListItem{
		{ID: uuid.Must(uuid.NewV7()), Status: "checkpoint", Label: "Before refactor"},
		{ID: uuid.Must(uuid.NewV7()), Status: "published"},
	}}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String()+"/versions", nil))
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, `"Label":"Before refactor"`) || !strings.Contains(body, `"Label":""`) {
		t.Fatalf("list must expose Label on every version: %d %s", w.Code, body)
	}
}

// W4: event managers edit exercises too, so capabilities and the flag policy
// are available to every authenticated caller (PermSelf); the deploy itself
// stays authorized per exercise.
func TestExerciseCapabilities_OnlyExposesLaboratoryAvailability(t *testing.T) {
	for _, tc := range []struct {
		role      rbac.Role
		available bool
		want      int
	}{
		{rbac.RoleAdmin, true, http.StatusOK},
		{rbac.RoleAdmin, false, http.StatusOK},
		{rbac.RoleUser, true, http.StatusOK},
		{rbac.RolePublic, true, http.StatusForbidden},
	} {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(response.WithErrorHandler)
		exerciseHandler.NewExerciseAPIHandler(&fakeUC{laboratoriesAvailable: tc.available}, roleProt{role: tc.role}).Init(r.Group("api"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/capabilities", nil))
		if w.Code != tc.want {
			t.Fatalf("role=%s available=%t: got %d, want %d; body=%s", tc.role, tc.available, w.Code, tc.want, w.Body.String())
		}
		if tc.want != http.StatusOK {
			continue
		}
		var body struct {
			Data struct {
				Laboratories         bool
				MaxActiveTestDeploys int
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Data.MaxActiveTestDeploys != 2 {
			t.Fatalf("the limit of active test labs must be exposed: %+v", body.Data)
		}
		if body.Data.Laboratories != tc.available {
			t.Fatalf("role=%s: laboratories=%t, want %t", tc.role, body.Data.Laboratories, tc.available)
		}
		if strings.Contains(w.Body.String(), "agents") {
			t.Fatalf("agent details leaked: %s", w.Body.String())
		}
	}
}

func TestExerciseFlagPolicy_ReturnsRuntimeValuesToAuthenticatedCallers(t *testing.T) {
	for _, tc := range []struct {
		role rbac.Role
		want int
	}{
		{rbac.RoleAdmin, http.StatusOK},
		{rbac.RoleAdminViewer, http.StatusOK},
		{rbac.RoleUser, http.StatusOK},
		{rbac.RolePublic, http.StatusForbidden},
	} {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(response.WithErrorHandler)
		exerciseHandler.NewExerciseAPIHandler(&fakeUC{flagPolicy: exerciseUseCase.FlagPolicy{RandomHexLength: 24, RandomBits: 96, WarningBits: 18}}, roleProt{role: tc.role}).Init(r.Group("api"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/flag-policy", nil))
		if w.Code != tc.want {
			t.Fatalf("role %s: got %d body=%s", tc.role, w.Code, w.Body.String())
		}
		if tc.want == http.StatusOK {
			var body struct{ Data exerciseUseCase.FlagPolicy }
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Data.RandomHexLength != 24 || body.Data.RandomBits != 96 || body.Data.WarningBits != 18 {
				t.Fatalf("wrong policy: %+v", body.Data)
			}
		}
	}
}

func TestListExercises_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		listResult: exerciseUseCase.ExercisesListResult{
			Exercises: []exerciseUseCase.ExerciseListItem{
				{ID: uuid.Must(uuid.NewV7()), Name: "nmap basics", Tags: []string{"recon"}},
			},
		},
	}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises?search=nmap&tags=recon", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Items []struct{ Name string }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Data.Items) != 1 || env.Data.Items[0].Name != "nmap basics" {
		t.Fatalf("unexpected exercises: %+v", env.Data.Items)
	}
}

func TestListExerciseTags_ReturnsPrefixCounts(t *testing.T) {
	uc := &fakeUC{tagSuggestions: []exerciseUseCase.ExerciseTagSuggestion{{Tag: "crypto", Count: 7}}}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/tags?prefix=Cr", nil))
	if w.Code != http.StatusOK || uc.tagPrefix != "Cr" {
		t.Fatalf("tag route: status=%d prefix=%q body=%s", w.Code, uc.tagPrefix, w.Body.String())
	}
	var env struct {
		Data []exerciseUseCase.ExerciseTagSuggestion
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Data) != 1 || env.Data[0].Tag != "crypto" || env.Data[0].Count != 7 {
		t.Fatalf("tag suggestions: %+v", env.Data)
	}
}

func TestListExerciseTags_EmptyPrefixAndLimit(t *testing.T) {
	uc := &fakeUC{tagSuggestions: []exerciseUseCase.ExerciseTagSuggestion{{Tag: "web", Count: 3}}}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/tags?limit=10", nil))
	if w.Code != http.StatusOK || uc.tagPrefix != "" || uc.tagLimit != 10 {
		t.Fatalf("top tags: status=%d prefix=%q limit=%d", w.Code, uc.tagPrefix, uc.tagLimit)
	}
	for _, bad := range []string{"0", "-1", "x"} {
		w = httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/tags?limit="+bad, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("limit=%s: status=%d", bad, w.Code)
		}
	}
}

func TestListExercises_ParsesSeveralEvents(t *testing.T) {
	a, b, c := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	url := "/api/exercises?page=1&pageSize=20&status=changed&event=" + a.String() + "," + b.String() + "&event=" + c.String() + "&event=" + a.String()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	got := uc.listFilter.EventIDs
	if len(got) != 3 || got[0] != a || got[1] != b || got[2] != c || uc.listFilter.Status != "changed" {
		t.Fatalf("events are repeated or comma-separated, deduplicated in order: %+v", uc.listFilter)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises?page=1&event="+a.String()+",nope", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("an invalid event id is a bad request: %d", w.Code)
	}
}

func TestListExercises_ItemCarriesStatusAndEvents(t *testing.T) {
	owner, access := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{listResult: exerciseUseCase.ExercisesListResult{Total: 1, Exercises: []exerciseUseCase.ExerciseListItem{{
		ID: uuid.Must(uuid.NewV7()), Name: "x", Status: "changed",
		ExerciseScopeView: exerciseUseCase.ExerciseScopeView{Scope: "event", OwnerEventID: &owner,
			OwnerEvent: &exerciseUseCase.EventRef{ID: owner, Name: "Owner"}, AccessEvents: []exerciseUseCase.EventRef{{ID: access, Name: "Guest"}}},
	}}}}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises?page=1", nil))
	var env struct {
		Data struct {
			Items []struct {
				Status     string
				OwnerEvent *struct {
					ID   uuid.UUID
					Name string
				}
				AccessEvents []struct {
					ID   uuid.UUID
					Name string
				}
			}
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	item := env.Data.Items[0]
	if item.Status != "changed" || item.OwnerEvent == nil || item.OwnerEvent.Name != "Owner" || len(item.AccessEvents) != 1 || item.AccessEvents[0].ID != access {
		t.Fatalf("list item: %s", w.Body.String())
	}
}

func TestListExercises_OffsetPagePreservesCursorEndpoint(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{listResult: exerciseUseCase.ExercisesListResult{Total: 52}}
	r := newEngine(uc, uid)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises?page=2&pageSize=25&sortBy=name&sortDir=asc&status=draft&tags=web", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("offset request: code=%d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Page, PageSize int
			Total          int64
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Page != 2 || env.Data.PageSize != 25 || env.Data.Total != 52 {
		t.Fatalf("offset envelope: %+v", env.Data)
	}
	if uc.listFilter.Page != 2 || uc.listFilter.SortBy != "name" || uc.listFilter.SortDir != "asc" || uc.listFilter.Status != "draft" || len(uc.listFilter.Tags) != 1 {
		t.Fatalf("offset filter: %+v", uc.listFilter)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises?pageSize=25", nil))
	if w.Code != http.StatusOK || uc.listFilter.Page != 0 {
		t.Fatalf("cursor compatibility: code=%d filter=%+v", w.Code, uc.listFilter)
	}
}

func TestCreateExercise_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{ID: exID, Name: "sqlmap 101"}}
	r := newEngine(uc, uid)

	body, _ := json.Marshal(map[string]any{"Name": "sqlmap 101", "Description": "d", "Tags": []string{"web"}})
	req := httptest.NewRequest(http.MethodPost, "/api/exercises", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct{ ID, Name string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.Name != "sqlmap 101" {
		t.Fatalf("unexpected response: %+v", env.Data)
	}
}

func TestExportOneExercise_UsesStandardPlatformZipName(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	id := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		exerciseView:    exerciseUseCase.ExerciseView{ID: id, Name: "SQL injection basics"},
		exportedArchive: []byte("zip contents"),
	}
	r := newEngine(uc, uid)
	body := strings.NewReader(`{"IDs":["` + id.String() + `"]}`)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/exercises/export", body)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Disposition"); !strings.Contains(got, "sql-injection-basics.cybericebox.zip") {
		t.Fatalf("unexpected archive disposition: %q", got)
	}
	if got := w.Header().Get("Content-Type"); !strings.Contains(got, "application/zip") {
		t.Fatalf("unexpected content type: %q", got)
	}
}

func TestExportExercise_RequiresExportPermission(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		role rbac.Role
		want int
	}{
		{role: rbac.RoleAdmin, want: http.StatusOK},
		{role: rbac.RoleAdminViewer, want: http.StatusForbidden},
	} {
		t.Run(string(tc.role), func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(response.WithErrorHandler)
			uc := &fakeUC{
				exerciseView:    exerciseUseCase.ExerciseView{ID: id, Name: "exercise"},
				exportedArchive: []byte("zip contents"),
			}
			exerciseHandler.NewExerciseAPIHandler(uc, roleProt{role: tc.role}).Init(r.Group("api"))
			req := httptest.NewRequest(http.MethodPost, "/api/exercises/export", strings.NewReader(`{"IDs":["`+id.String()+`"]}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("export status = %d, want %d", w.Code, tc.want)
			}
		})
	}
}

func TestExportSeveralExercises_UsesFoldersInsteadOfNestedArchives(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	archive, err := exerciseUseCase.BuildArchiveV1(map[string][]byte{
		"exercise.json": []byte(`{"format":"cib-exercise/v1"}`),
		"files/a.txt":   []byte("attachment"),
	})
	if err != nil {
		t.Fatalf("build archive: %v", err)
	}
	uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{Name: "web basics"}, exportedArchive: archive}
	r := newEngine(uc, uid)
	body := strings.NewReader(`{"IDs":["` + first.String() + `","` + second.String() + `"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/exercises/export", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	files, err := exerciseUseCase.ReadArchiveV1(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("read bundle: %v", err)
	}
	if string(files["web-basics/exercise.json"]) == "" || string(files["web-basics-2/exercise.json"]) == "" {
		t.Fatalf("expected exercise folders, got entries: %#v", files)
	}
	for name := range files {
		if strings.HasSuffix(name, ".zip") {
			t.Fatalf("bundle must not contain nested zip archive: %s", name)
		}
	}
}

func TestGetExercise_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{ID: exID, Name: "x"}}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetExercise_BadID_Returns400(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{}, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/not-a-uuid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestGetExercise_NotFound_Returns404(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{err: exerciseModel.ErrExerciseNotFound.Err()}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUpdateExercise_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{ID: exID, Name: "renamed"}}
	r := newEngine(uc, uid)

	body, _ := json.Marshal(map[string]any{"Name": "renamed", "Description": "d", "Tags": []string{}})
	req := httptest.NewRequest(http.MethodPatch, "/api/exercises/"+exID.String(), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestDeleteExercise_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{}, uid)

	req := httptest.NewRequest(http.MethodDelete, "/api/exercises/"+exID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestListVersions_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{versionList: []exerciseUseCase.VersionListItem{{ID: uuid.Must(uuid.NewV7()), Status: "draft"}}}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String()+"/versions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data []struct{ Status string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Data) != 1 || env.Data[0].Status != "draft" {
		t.Fatalf("unexpected versions: %+v", env.Data)
	}
}

func TestGetVersion_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	versionID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{versionView: exerciseUseCase.VersionView{ID: versionID, ExerciseID: exID, Status: "published"}}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String()+"/versions/"+versionID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestSaveDraft_MapsFlagAsPlainStringSlice locks in deviation 1: Task.Flag is
// a plain []string (no FlagSource/FlagMode wrapper) end to end through the
// HTTP DTO.
func TestSaveDraft_MapsFlagAsPlainStringSlice(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{versionView: exerciseUseCase.VersionView{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID}}
	r := newEngine(uc, uid)

	reqBody := map[string]any{
		"AdminNote": "note",
		"Variants": []map[string]any{
			{
				"Index": 0,
				"Tasks": []map[string]any{
					{"Name": "find the flag", "Difficulty": "easy", "Flag": []string{"FLAG{one}", "FLAG{two}"}},
				},
				"Topology": map[string]any{
					"VPN":      map[string]any{"Enabled": false, "DHCP": false},
					"Internet": map[string]any{"Enabled": false, "DHCP": false},
				},
			},
		},
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPut, "/api/exercises/"+exID.String()+"/draft", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if len(uc.savedVariants) != 1 || len(uc.savedVariants[0].Tasks) != 1 {
		t.Fatalf("unexpected saved variants: %+v", uc.savedVariants)
	}
	gotFlag := uc.savedVariants[0].Tasks[0].Flag
	if len(gotFlag) != 2 || gotFlag[0] != "FLAG{one}" || gotFlag[1] != "FLAG{two}" {
		t.Fatalf("Flag not mapped as plain []string: %+v", gotFlag)
	}
}

// TestSaveDraft_RejectsWrongJSONShape is a regression guarantee: business
// validation moved to publish-only (Task 2), but malformed JSON shape must
// still be rejected by HTTP binding before it ever reaches the use case.
func TestSaveDraft_RejectsWrongJSONShape(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	req := httptest.NewRequest(http.MethodPut, "/api/exercises/"+exID.String()+"/draft", strings.NewReader(`{"Variants":"not-a-list"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest || uc.savedVariants != nil {
		t.Fatalf("shape errors must stay 400: code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestDiscardDraft_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{}, uid)

	req := httptest.NewRequest(http.MethodDelete, "/api/exercises/"+exID.String()+"/draft", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPublish_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{versionView: exerciseUseCase.VersionView{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "published"}}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodPost, "/api/exercises/"+exID.String()+"/publish", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestPublish_NoDraft_ReturnsConflict(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{err: exerciseModel.ErrNoDraft.Err()}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodPost, "/api/exercises/"+exID.String()+"/publish", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestRollback_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	versionID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{versionView: exerciseUseCase.VersionView{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID}}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodPost, "/api/exercises/"+exID.String()+"/versions/"+versionID.String()+"/rollback", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUploadFile_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{uploadedFile: mediaModel.File{ID: fileID, Name: "notes.txt", SizeBytes: 5}}
	r := newEngine(uc, uid)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "notes.txt")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err = part.Write([]byte("hello")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/exercises/files", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if uc.uploadName != "notes.txt" || uc.uploadContent != "hello" {
		t.Fatalf("upload not forwarded correctly: name=%q content=%q", uc.uploadName, uc.uploadContent)
	}
	if uc.uploadCreatedBy != uid {
		t.Fatalf("want createdBy=%s, got %s", uid, uc.uploadCreatedBy)
	}
	var env struct {
		Data struct {
			FileID uuid.UUID
			Name   string
			Size   int64
		}
	}
	if err = json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.FileID != fileID || env.Data.Name != "notes.txt" || env.Data.Size != 5 {
		t.Fatalf("unexpected response: %+v", env.Data)
	}
}

func TestUploadFile_MissingFile_Returns400(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{}, uid)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.Close() // no "file" part

	req := httptest.NewRequest(http.MethodPost, "/api/exercises/files", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestUploadFile_OverCap_Returns4xxNot500 is the regression test for the
// HTTP-boundary size limit: before the fix, ctx.FormFile parsed the ENTIRE
// multipart body into memory/temp files before the use case's own LimitReader
// ever got a chance to reject an oversized upload — an attacker could exhaust
// memory/disk with a huge body regardless of the configured cap. Wrapping
// ctx.Request.Body in http.MaxBytesReader (sized off MaxUploadBytes, before
// FormFile parses anything) must reject the request with a clean 4xx, never
// a raw 500 from an unhandled parse failure.
func TestUploadFile_OverCap_Returns4xxNot500(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	// A tiny configured cap makes it trivial to build a request body that
	// exceeds cap+multipartOverheadSlack without a multi-megabyte fixture.
	uc := &fakeUC{maxUploadBytes: 10}
	r := newEngine(uc, uid)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, err := w.CreateFormFile("file", "big.bin")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// 1 MiB slack + 10-byte cap: two MiB of payload is comfortably over the cap.
	oversized := bytes.Repeat([]byte("A"), 2<<20)
	if _, err = part.Write(oversized); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err = w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/exercises/files", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code >= 500 {
		t.Fatalf("oversized upload must not surface as a 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Code < 400 || rec.Code >= 500 {
		t.Fatalf("want a 4xx for an over-cap upload, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDownloadFile_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		streamedFile:    mediaModel.File{ID: fileID, Name: "attachment.bin", ContentType: "application/pdf", SizeBytes: 5},
		streamedContent: "hello",
	}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/files/"+fileID.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("unexpected body: %q", rec.Body.String())
	}
	mediatype, params, err := mime.ParseMediaType(rec.Header().Get("Content-Disposition"))
	if err != nil {
		t.Fatalf("Content-Disposition not parseable: %q: %v", rec.Header().Get("Content-Disposition"), err)
	}
	if mediatype != "attachment" || params["filename"] != "attachment.bin" {
		t.Fatalf("unexpected Content-Disposition: %q", rec.Header().Get("Content-Disposition"))
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Fatalf("unexpected Content-Type: %q", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("want X-Content-Type-Options=nosniff, got %q", got)
	}
	if uc.streamedFileID != fileID {
		t.Fatalf("want streamed fileID=%s, got %s", fileID, uc.streamedFileID)
	}
}

// TestDownloadFile_QuoteInName_EscapesContentDisposition locks in that a
// client-supplied filename containing a double quote cannot break out of the
// Content-Disposition quoted-string (header parameter injection): the header
// must stay machine-parseable and round-trip back to the exact stored name.
func TestDownloadFile_QuoteInName_EscapesContentDisposition(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		streamedFile:    mediaModel.File{ID: fileID, Name: `foo".txt`, ContentType: "text/plain", SizeBytes: 5},
		streamedContent: "hello",
	}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/files/"+fileID.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	// A raw unescaped quote after "foo" is exactly the quoted-string breakout
	// the hand-built header produced: filename="foo".txt".
	if strings.Contains(cd, `foo"`) && !strings.Contains(cd, `foo\"`) {
		t.Fatalf("unescaped quote breakout in Content-Disposition: %q", cd)
	}
	mediatype, params, err := mime.ParseMediaType(cd)
	if err != nil {
		t.Fatalf("Content-Disposition not parseable: %q: %v", cd, err)
	}
	if mediatype != "attachment" {
		t.Fatalf("want attachment disposition, got %q (header %q)", mediatype, cd)
	}
	if params["filename"] != `foo".txt` {
		t.Fatalf("filename did not round-trip: got %q (header %q)", params["filename"], cd)
	}
}

func TestDownloadFile_BadID_Returns400(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{}, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/files/not-a-uuid", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDownloadFile_NotFound_Returns404(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{err: mediaModel.ErrFileNotFound.Err()}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/files/"+fileID.String(), nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d body=%s", rec.Code, rec.Body.String())
	}
}

// TestEveryRouteRequiresPermission is a smoke test that every route in
// Init() is reachable only through the protection gate — the fakeProt above
// is a pass-through, so this really just documents which routes exist and
// that none panics when wired; the actual "every route gated" invariant is
// enforced statically by tools/checkroutes (make vet).
func TestEveryRouteRequiresPermission(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	exID := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{
		exerciseView: exerciseUseCase.ExerciseView{ID: exID},
		versionView:  exerciseUseCase.VersionView{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID},
		streamedFile: mediaModel.File{ID: fileID, Name: "f"},
	}
	r := newEngine(uc, uid)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/exercises"},
		{http.MethodGet, "/api/exercises/" + exID.String()},
		{http.MethodGet, "/api/exercises/" + exID.String() + "/versions"},
		// Regression coverage for the routing note: "files" is a static
		// segment registered before ":id" so it must resolve to downloadFile,
		// not clash/shadow with the ":id" param route.
		{http.MethodGet, "/api/exercises/files/" + fileID.String()},
	}
	for _, rt := range routes {
		req := httptest.NewRequest(rt.method, rt.path, nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("%s %s: want 200, got %d body=%s", rt.method, rt.path, w.Code, w.Body.String())
		}
	}
}

func TestDeployVariant_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{deployHandle: exerciseModel.DeployHandle{Group: "t-123", Lab: "lab", VPNClient: "tester"}}
	r := newEngine(uc, uid)

	ex := uuid.Must(uuid.NewV7())
	ver := uuid.Must(uuid.NewV7())
	variant := uuid.Must(uuid.NewV7())
	url := "/api/exercises/" + ex.String() + "/versions/" + ver.String() + "/variants/" + variant.String() + "/deploy"
	req := httptest.NewRequest(http.MethodPost, url, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			DeployID  string
			VPNClient string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.DeployID != "t-123" || env.Data.VPNClient != "tester" {
		t.Errorf("deploy response wrong: %+v", env.Data)
	}
}

func TestDeployStatus_Returns200(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{deployStatus: exerciseModel.LabDeployStatus{
		Phase: "Ready", Ready: true,
		Access: []exerciseModel.LabAccess{{Device: "web", Port: 443, URL: "https://web.lab"}},
	}}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/deploys/"+uuid.Must(uuid.NewV7()).String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data struct {
			Phase  string
			Ready  bool
			Access []struct{ URL string }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Phase != "Ready" || !env.Data.Ready || len(env.Data.Access) != 1 || env.Data.Access[0].URL != "https://web.lab" {
		t.Errorf("status response wrong: %+v", env.Data)
	}
}

func TestDeployStatus_ReportsVPNConnectionAndSolvedTasks(t *testing.T) {
	uid, solved := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	at := time.Date(2026, 9, 30, 12, 0, 5, 0, time.UTC)
	uc := &fakeUC{deployStatus: exerciseModel.LabDeployStatus{Phase: "Ready", VPNConnected: true, VPNLastHandshake: at, SolvedTasks: []uuid.UUID{solved}}}
	w := httptest.NewRecorder()
	newEngine(uc, uid).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/deploys/"+uuid.Must(uuid.NewV7()).String(), nil))
	var env struct {
		Data struct {
			VPNConnected     bool
			VPNLastHandshake *time.Time
			SolvedTaskIDs    []string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if !env.Data.VPNConnected || env.Data.VPNLastHandshake == nil || !env.Data.VPNLastHandshake.Equal(at) || len(env.Data.SolvedTaskIDs) != 1 || env.Data.SolvedTaskIDs[0] != solved.String() {
		t.Errorf("vpn wrong: %s", w.Body.String())
	}
	uc.deployStatus = exerciseModel.LabDeployStatus{Phase: "Ready"}
	w = httptest.NewRecorder()
	newEngine(uc, uid).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/deploys/"+uuid.Must(uuid.NewV7()).String(), nil))
	if strings.Contains(w.Body.String(), "VPNLastHandshake") || !strings.Contains(w.Body.String(), `"SolvedTaskIDs":[]`) {
		t.Errorf("a never connected client has no handshake: %s", w.Body.String())
	}
}

func TestListDeploys_FiltersAndNeverReturnsFlagValues(t *testing.T) {
	uid, exID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	keep := exerciseModel.TestDeploy{LabName: "l-abc", ID: uuid.Must(uuid.NewV7()), VersionID: uuid.Must(uuid.NewV7()), VariantID: uuid.Must(uuid.NewV7()),
		Flags: []exerciseModel.DeployFlag{{TaskID: uuid.Must(uuid.NewV7()), Name: "Login", Flag: "ICE{a}"}}}
	other := exerciseModel.TestDeploy{ID: uuid.Must(uuid.NewV7()), VersionID: uuid.Must(uuid.NewV7()), VariantID: uuid.Must(uuid.NewV7())}
	uc := &fakeUC{testDeploys: []exerciseModel.TestDeploy{keep, other}}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodGet, "/api/exercises/deploys?exerciseID="+exID.String()+"&versionID="+keep.VersionID.String(), nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	var env struct {
		Data []struct {
			DeployID string
			Lab      string
			Tasks    []struct{ Name string }
		}
	}
	if strings.Contains(w.Body.String(), "ICE{a}") {
		t.Fatalf("a flag value leaked: %s", w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if uc.listDeploysExercise != exID || len(env.Data) != 1 || env.Data[0].DeployID != keep.ID.String() || env.Data[0].Lab != "l-abc" ||
		len(env.Data[0].Tasks) != 1 || env.Data[0].Tasks[0].Name != "Login" {
		t.Errorf("list wrong: exercise=%v %+v", uc.listDeploysExercise, env.Data)
	}

	bad := httptest.NewRecorder()
	r.ServeHTTP(bad, httptest.NewRequest(http.MethodGet, "/api/exercises/deploys?exerciseID=nope", nil))
	if bad.Code != http.StatusBadRequest {
		t.Errorf("bad exercise id: want 400, got %d", bad.Code)
	}
}

func TestCheckDeploy_AnswersOnlyCorrectOrNot(t *testing.T) {
	r := newEngine(&fakeUC{}, uuid.Must(uuid.NewV7()))
	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+uuid.Must(uuid.NewV7()).String()+"/check", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	task := uuid.Must(uuid.NewV7()).String()
	for flag, want := range map[string]bool{"ICE{ok}": true, "ICE{OK}": false} {
		w := post(`{"TaskID":"` + task + `","Flag":"` + flag + `"}`)
		var env struct{ Data struct{ Correct bool } }
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &env) != nil || env.Data.Correct != want {
			t.Errorf("flag %q: code=%d body=%s want correct=%v", flag, w.Code, w.Body.String(), want)
		}
	}
	if w := post(`{"TaskID":"nope","Flag":"x"}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad task id: want 400, got %d", w.Code)
	}
}

func TestVersionResponse_HasNoRegenerateFlagsField(t *testing.T) {
	exID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUC{versionView: exerciseUseCase.VersionView{ID: versionID, ExerciseID: exID, Status: "draft"}}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String()+"/versions/"+versionID.String(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "RegenerateFlagsOnPublish") {
		t.Fatalf("version response must not carry RegenerateFlagsOnPublish: %s", w.Body.String())
	}
}

func TestListExercises_ArchivedFilterAndField(t *testing.T) {
	archivedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	uc := &fakeUC{listResult: exerciseUseCase.ExercisesListResult{Exercises: []exerciseUseCase.ExerciseListItem{
		{ID: uuid.Must(uuid.NewV7()), Name: "old one", ArchivedAt: &archivedAt},
		{ID: uuid.Must(uuid.NewV7()), Name: "live one"},
	}}}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	for _, url := range []string{"/api/exercises?archived=only", "/api/exercises?page=1&archived=only"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, url, nil))
		if w.Code != http.StatusOK || uc.listFilter.Archived != "only" {
			t.Fatalf("%s: code=%d filter=%+v", url, w.Code, uc.listFilter)
		}
		var env struct {
			Data struct {
				Items []struct {
					Name       string
					ArchivedAt *time.Time
				}
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if len(env.Data.Items) != 2 || env.Data.Items[0].ArchivedAt == nil || !env.Data.Items[0].ArchivedAt.Equal(archivedAt) || env.Data.Items[1].ArchivedAt != nil {
			t.Fatalf("%s: items %+v", url, env.Data.Items)
		}
		if !strings.Contains(w.Body.String(), `"ArchivedAt":null`) {
			t.Fatalf("%s: active rows must serialize ArchivedAt as null: %s", url, w.Body.String())
		}
	}
}

func TestGetExercise_SerializesHasChangesAndArchivedAt(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{ID: exID, Name: "card", HasChanges: true}}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String(), nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, `"HasChanges":true`) || !strings.Contains(body, `"ArchivedAt":null`) {
		t.Fatalf("card must carry HasChanges and ArchivedAt: %s", body)
	}
}

func TestArchiveRoutes_ReturnCardAndRequireWrite(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	archivedAt := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		role   rbac.Role
		action string
		want   int
	}{
		{rbac.RoleAdmin, "archive", http.StatusOK},
		{rbac.RoleAdmin, "unarchive", http.StatusOK},
		{rbac.RoleAdminViewer, "archive", http.StatusForbidden},
		{rbac.RoleAdminViewer, "unarchive", http.StatusForbidden},
	} {
		t.Run(string(tc.role)+"/"+tc.action, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(response.WithErrorHandler)
			r.Use(injectIdentity(uuid.Must(uuid.NewV7())))
			uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{ID: exID, Name: "card", ArchivedAt: &archivedAt}}
			exerciseHandler.NewExerciseAPIHandler(uc, roleProt{role: tc.role}).Init(r.Group("api"))
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/exercises/"+exID.String()+"/"+tc.action, nil))
			if w.Code != tc.want {
				t.Fatalf("got %d, want %d; body=%s", w.Code, tc.want, w.Body.String())
			}
			if tc.want != http.StatusOK {
				return
			}
			called := uc.archivedID
			if tc.action == "unarchive" {
				called = uc.unarchivedID
			}
			if called != exID || !strings.Contains(w.Body.String(), `"ArchivedAt":"2026-09-01T12:00:00Z"`) {
				t.Fatalf("called=%s body=%s", called, w.Body.String())
			}
		})
	}
}

func TestGetDraft_ReturnsWorkingCopyForReaders(t *testing.T) {
	exID, publishedID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		role rbac.Role
		want int
	}{{rbac.RoleAdminViewer, http.StatusOK}, {rbac.RoleUser, http.StatusForbidden}} {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(response.WithErrorHandler)
		uc := &fakeUC{versionView: exerciseUseCase.VersionView{ID: publishedID, ExerciseID: exID, Status: "published", Variants: []exerciseModel.Variant{}}}
		exerciseHandler.NewExerciseAPIHandler(uc, roleProt{role: tc.role}).Init(r.Group("api"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String()+"/draft", nil))
		if w.Code != tc.want {
			t.Fatalf("role %s: got %d body=%s", tc.role, w.Code, w.Body.String())
		}
		if tc.want == http.StatusOK && (!strings.Contains(w.Body.String(), `"Status":"published"`) || !strings.Contains(w.Body.String(), publishedID.String())) {
			t.Fatalf("unexpected working copy: %s", w.Body.String())
		}
	}
}

func TestSaveDraft_ArchivedIsConflict(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{err: exerciseModel.ErrExerciseArchived.Err()}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	req := httptest.NewRequest(http.MethodPut, "/api/exercises/"+exID.String()+"/draft", strings.NewReader(`{"Variants":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", w.Code, w.Body.String())
	}
}

func TestUsage_ReturnsEventsForReaders(t *testing.T) {
	exID, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, tc := range []struct {
		role rbac.Role
		want int
	}{{rbac.RoleAdminViewer, http.StatusOK}, {rbac.RoleUser, http.StatusForbidden}} {
		gin.SetMode(gin.TestMode)
		r := gin.New()
		r.Use(response.WithErrorHandler)
		uc := &fakeUC{usage: exerciseUseCase.ExerciseUsage{Events: []exerciseUseCase.ExerciseUsageEvent{{ID: eventID, Name: "Spring CTF", Archived: true}}}}
		exerciseHandler.NewExerciseAPIHandler(uc, roleProt{role: tc.role}).Init(r.Group("api"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+exID.String()+"/usage", nil))
		if w.Code != tc.want {
			t.Fatalf("role %s: got %d body=%s", tc.role, w.Code, w.Body.String())
		}
		if tc.want != http.StatusOK {
			continue
		}
		var env struct {
			Data struct {
				Events []struct {
					ID       uuid.UUID
					Name     string
					Archived bool
				}
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatal(err)
		}
		if len(env.Data.Events) != 1 || env.Data.Events[0].ID != eventID || env.Data.Events[0].Name != "Spring CTF" || !env.Data.Events[0].Archived {
			t.Fatalf("usage body: %s", w.Body.String())
		}
	}
}

func TestUsage_EmptyIsList(t *testing.T) {
	r := newEngine(&fakeUC{}, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/"+uuid.Must(uuid.NewV7()).String()+"/usage", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Events":[]`) {
		t.Fatalf("empty usage must be [], got %d %s", w.Code, w.Body.String())
	}
}

func TestDelete_InUseIsConflict(t *testing.T) {
	uc := &fakeUC{err: exerciseModel.ErrExerciseInUse.Err()}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/exercises/"+uuid.Must(uuid.NewV7()).String(), nil))
	if w.Code != http.StatusConflict {
		t.Fatalf("want 409, got %d body=%s", w.Code, w.Body.String())
	}
}

// ── W4 fakes: the policy grants what the caller's RBAC role grants (the
// data-dependent membership rules are covered by use-case tests).
func fakeActionPermission(action exerciseUseCase.Action) rbac.Permission {
	switch action {
	case exerciseUseCase.ActionWrite:
		return rbac.PermExercisesWrite
	case exerciseUseCase.ActionPublish:
		return rbac.PermExercisesPublish
	case exerciseUseCase.ActionDelete:
		return rbac.PermExercisesDelete
	default:
		return rbac.PermExercisesRead
	}
}

func (f *fakeUC) AuthorizeExercise(_ context.Context, actor exerciseUseCase.Actor, _ uuid.UUID, action exerciseUseCase.Action) (exerciseUseCase.Access, error) {
	if !actor.Role.HasPermission(fakeActionPermission(action)) {
		return exerciseUseCase.Access{}, exerciseModel.ErrExerciseForbidden.Err()
	}
	return exerciseUseCase.Access{Full: true}, nil
}
func (f *fakeUC) AuthorizeFileUpload(_ context.Context, actor exerciseUseCase.Actor) error {
	if !actor.Role.HasPermission(rbac.PermExercisesWrite) {
		return exerciseModel.ErrExerciseForbidden.Err()
	}
	return nil
}
func (f *fakeUC) AuthorizeFileDownload(_ context.Context, actor exerciseUseCase.Actor, _ mediaModel.File) error {
	if !actor.Role.HasPermission(rbac.PermExercisesRead) {
		return mediaModel.ErrFileNotFound.Err()
	}
	return nil
}
func (f *fakeUC) AuthorizeTestDeploy(ctx context.Context, actor exerciseUseCase.Actor, exerciseID, _ uuid.UUID) error {
	_, err := f.AuthorizeExercise(ctx, actor, exerciseID, exerciseUseCase.ActionWrite)
	return err
}
func (f *fakeUC) GetAccessSummary(_ context.Context, actor exerciseUseCase.Actor) (exerciseUseCase.AccessSummary, error) {
	return exerciseUseCase.AccessSummary{IsAdmin: actor.Role.HasPermission(rbac.PermExercisesRead)}, f.err
}
func (f *fakeUC) CreateExerciseFor(ctx context.Context, _ exerciseUseCase.Actor, in exerciseUseCase.CreateExerciseInput) (exerciseUseCase.ExerciseView, error) {
	return f.CreateExercise(ctx, in)
}
func (f *fakeUC) GetExerciseFor(ctx context.Context, _ exerciseUseCase.Actor, id uuid.UUID) (exerciseUseCase.ExerciseView, error) {
	return f.GetExercise(ctx, id)
}
func (f *fakeUC) ListExercisesFor(ctx context.Context, _ exerciseUseCase.Actor, filter exerciseUseCase.ExercisesFilter) (exerciseUseCase.ExercisesListResult, error) {
	return f.ListExercises(ctx, filter)
}
func (f *fakeUC) SetExerciseAccess(_ context.Context, _ exerciseUseCase.Actor, _ uuid.UUID, in exerciseUseCase.SetAccessInput) (exerciseUseCase.ExerciseView, error) {
	f.accessInput = in
	return f.exerciseView, f.err
}
func (f *fakeUC) ProposeExercise(_ context.Context, _ exerciseUseCase.Actor, _ uuid.UUID, _ string) (exerciseUseCase.ProposalView, error) {
	return exerciseUseCase.ProposalView{}, f.err
}
func (f *fakeUC) ListProposals(_ context.Context, _ string) ([]exerciseUseCase.ProposalView, error) {
	return []exerciseUseCase.ProposalView{}, f.err
}
func (f *fakeUC) ApproveProposal(_ context.Context, _ exerciseUseCase.Actor, _ uuid.UUID, _ exerciseUseCase.ApproveProposalInput) (exerciseUseCase.ProposalView, error) {
	return exerciseUseCase.ProposalView{}, f.err
}
func (f *fakeUC) RejectProposal(_ context.Context, _ exerciseUseCase.Actor, _ uuid.UUID, _ string) (exerciseUseCase.ProposalView, error) {
	return exerciseUseCase.ProposalView{}, f.err
}

func TestSetExerciseAccess_NoneLevel(t *testing.T) {
	exID := uuid.Must(uuid.NewV7())
	uc := &fakeUC{exerciseView: exerciseUseCase.ExerciseView{ID: exID, ExerciseScopeView: exerciseUseCase.ExerciseScopeView{Scope: "catalog", AccessLevel: "none"}}}
	r := newEngine(uc, uuid.Must(uuid.NewV7()))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/exercises/"+exID.String()+"/access", strings.NewReader(`{"AccessLevel":"none","EventIDs":[]}`)))
	if w.Code != http.StatusOK || uc.accessInput.AccessLevel != exerciseModel.AccessNone || !strings.Contains(w.Body.String(), `"AccessLevel":"none"`) {
		t.Fatalf("none access: status=%d input=%+v body=%s", w.Code, uc.accessInput, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/exercises/"+exID.String()+"/access", strings.NewReader(`{"AccessLevel":"nobody"}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("unknown level: status=%d", w.Code)
	}
}

func TestDeployLink_ReturnsTheLinkAndSetsNoCookie(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	expires := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	uc := &fakeUC{sessionLink: labaccess.Link{URL: "https://web-abc123.challenges.example.com/_auth?t=signed", Token: "signed", ExpiresAt: expires}}
	r := newEngine(uc, uid)

	req := httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+uuid.Must(uuid.NewV7()).String()+"/link", strings.NewReader(`{"Device":"web","Port":80}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	if len(w.Result().Cookies()) != 0 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("cookie/headers: %+v %v", w.Result().Cookies(), w.Header())
	}
	var env struct {
		Data struct {
			URL       string
			ExpiresAt time.Time
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || !env.Data.ExpiresAt.Equal(expires) || env.Data.URL != uc.sessionLink.URL {
		t.Fatalf("body: %s err=%v", w.Body.String(), err)
	}
}

func TestDeployLink_ForeignOrNotReadyIsAnError(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	r := newEngine(&fakeUC{err: exerciseModel.ErrTestDeployNotFound.Err()}, uid)
	req := httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+uuid.Must(uuid.NewV7()).String()+"/link", strings.NewReader(`{"Device":"web","Port":80}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound || len(w.Result().Cookies()) != 0 {
		t.Fatalf("want 404 without a cookie, got %d", w.Code)
	}
}

func TestDeployStatus_ExposesQueueSnapshotAndImageWarnings(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{deployStatus: exerciseModel.LabDeployStatus{
		Phase: exerciseModel.DeployPhaseQueued, ImageWarning: "web:latest", GroupImageWarning: "vpn:1",
		Queue:   &exerciseModel.LabQueue{Position: 3, Length: 8, Reason: exerciseModel.QueueReasonNoSchedulableNodes},
		Devices: []exerciseModel.LabDeployedDevice{{Name: "web", Snapshot: &exerciseModel.DeviceSnapshot{SizeBytes: 5, Rescue: true}}, {Name: "plain"}},
	}}
	w := httptest.NewRecorder()
	newEngine(uc, uid).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/exercises/deploys/"+uuid.Must(uuid.NewV7()).String(), nil))
	for _, want := range []string{`"Phase":"Queued"`, `"Queue":{"Position":3,"Length":8,"Reason":"NoSchedulableNodes","Message":"","Pods":0,"Pending":0}`, `"ImageWarning":"web:latest"`, `"GroupImageWarning":"vpn:1"`,
		`"Snapshot":{"LastSnapshotAt":null,"RestoredAt":null,"SizeBytes":5,"Warning":"","Rescue":true}`, `{"Name":"plain","Ready":false,"Reason":"","Scheduling":null,"Snapshot":null}`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("status lacks %s: %s", want, w.Body.String())
		}
	}
}

func TestDeviceActions_UseTheCallerAndMapErrors(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc := &fakeUC{}
	r := newEngine(uc, uid)
	deploy := uuid.Must(uuid.NewV7()).String()

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+deploy+"/devices/web/reset", nil))
	if w.Code != http.StatusOK || uc.deviceCall != "reset web by "+uid.String() {
		t.Fatalf("reset: %d %q", w.Code, uc.deviceCall)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+deploy+"/devices/web/rescue", strings.NewReader(`{"Enable":true}`)))
	if w.Code != http.StatusOK || uc.deviceCall != "rescue web true" {
		t.Fatalf("rescue: %d %q", w.Code, uc.deviceCall)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+deploy+"/devices/web/rescue", strings.NewReader(`nope`)))
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad body -> %d, want 400", w.Code)
	}
	uc.err = infraModel.ErrDeviceNotPersistent.Err()
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/exercises/deploys/"+deploy+"/devices/web/reset", nil))
	if w.Code != http.StatusConflict {
		t.Errorf("no persistence -> %d, want 409", w.Code)
	}
}
