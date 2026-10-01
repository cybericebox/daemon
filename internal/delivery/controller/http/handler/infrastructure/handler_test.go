package infrastructure

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/infrastructure/agentfleet"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	infrastructureUseCase "github.com/cybericebox/daemon/internal/useCase/infrastructure"
)

type fakeUseCase struct {
	includeRecent *bool
	pageSize      int32
	standsFilter  infrastructureUseCase.StandsFilter
	recreated     [3]uuid.UUID
	recreateErr   error
	terminated    uuid.UUID
	terminateErr  error
	testLabsQuery [3]any
	observations  infrastructureUseCase.Page[infrastructureUseCase.LabMonitoringView]
	deviceCall    string
	deviceErr     error
	agentsView    infrastructureUseCase.AgentsView
	enrollInput   infraModel.AgentEnrollment
	listArchived  bool
	preview       infrastructureUseCase.DeletePreview
	reconnect     string
	confirm       bool
	updateInput   infraModel.AgentUpdate
	agentID       uuid.UUID
	agentErr      error
}

func (f *fakeUseCase) ListAgents(_ context.Context, includeArchived bool) (infrastructureUseCase.AgentsView, error) {
	f.listArchived = includeArchived
	return f.agentsView, f.agentErr
}
func (f *fakeUseCase) PreviewAgentDelete(_ context.Context, id uuid.UUID) (infrastructureUseCase.DeletePreview, error) {
	f.agentID = id
	return f.preview, f.agentErr
}
func (f *fakeUseCase) ReconnectAgent(_ context.Context, id uuid.UUID, token, caPEM string) (infrastructureUseCase.AgentAdminView, error) {
	f.agentID, f.reconnect = id, token+"|"+caPEM
	return infrastructureUseCase.AgentAdminView{AgentRegistration: infraModel.AgentRegistration{ID: id, Tenant: "platform"}}, f.agentErr
}
func (f *fakeUseCase) EnrollAgent(_ context.Context, in infraModel.AgentEnrollment) (infrastructureUseCase.AgentAdminView, error) {
	f.enrollInput = in
	return infrastructureUseCase.AgentAdminView{AgentRegistration: infraModel.AgentRegistration{ID: uuid.Must(uuid.NewV7()), Name: in.Name, Source: infraModel.AgentSourceAdmin, Endpoint: in.Endpoint, Enabled: in.Enabled, Priority: in.Priority, Tenant: "platform"}, InUse: true}, f.agentErr
}
func (f *fakeUseCase) UpdateAgent(_ context.Context, id uuid.UUID, in infraModel.AgentUpdate) (infrastructureUseCase.AgentAdminView, error) {
	f.agentID, f.updateInput = id, in
	return infrastructureUseCase.AgentAdminView{AgentRegistration: infraModel.AgentRegistration{ID: id, Name: in.Name}}, f.agentErr
}
func (f *fakeUseCase) RenewAgentCertificate(_ context.Context, id uuid.UUID) (infrastructureUseCase.AgentAdminView, error) {
	f.agentID, f.deviceCall = id, "renew"
	return infrastructureUseCase.AgentAdminView{AgentRegistration: infraModel.AgentRegistration{ID: id}}, f.agentErr
}
func (f *fakeUseCase) RotateAgentAccessKey(_ context.Context, id uuid.UUID) (infrastructureUseCase.AgentAdminView, error) {
	f.agentID, f.deviceCall = id, "rotate"
	return infrastructureUseCase.AgentAdminView{AgentRegistration: infraModel.AgentRegistration{ID: id, AccessKeyID: "k-new"}, RetiredKeys: 1}, f.agentErr
}
func (f *fakeUseCase) DeleteAgent(_ context.Context, id uuid.UUID, confirm bool) error {
	f.agentID, f.confirm = id, confirm
	return f.agentErr
}
func (f *fakeUseCase) CheckAgent(_ context.Context, id uuid.UUID) (infrastructureUseCase.AgentAdminView, error) {
	f.agentID = id
	return infrastructureUseCase.AgentAdminView{AgentRegistration: infraModel.AgentRegistration{ID: id}, Probe: agentfleet.AgentProbe{Connected: true, Healthy: true, Latency: 1500 * time.Microsecond}}, f.agentErr
}

func (f *fakeUseCase) InfrastructureStatus(context.Context) (infrastructureUseCase.Status, error) {
	return infrastructureUseCase.Status{Connected: true, Healthy: true, Mode: "available", Capabilities: infrastructureUseCase.Capabilities{Laboratories: true}}, nil
}
func (f *fakeUseCase) CurrentLabMonitoring(_ context.Context, includeRecent bool) ([]infrastructureUseCase.CurrentLabView, error) {
	f.includeRecent = &includeRecent
	return []infrastructureUseCase.CurrentLabView{
		{EventName: "CTF", TeamName: "moderators:x", Moderators: true, Payload: json.RawMessage(`{"labs":[{"name":"web"}]}`)},
	}, nil
}
func (f *fakeUseCase) ListLabMonitoring(_ context.Context, _, _ uuid.NullUUID, _, _, _ time.Time, _ uuid.UUID, pageSize int32) (infrastructureUseCase.Page[infrastructureUseCase.LabMonitoringView], error) {
	f.pageSize = pageSize
	return f.observations, nil
}
func (f *fakeUseCase) CurrentCapacityMonitoring(context.Context) ([]infrastructureUseCase.CapacityMonitoringView, error) {
	return nil, nil
}
func (f *fakeUseCase) ListCapacityMonitoring(context.Context, time.Time, time.Time, time.Time, uuid.UUID, int32) (infrastructureUseCase.Page[infrastructureUseCase.CapacityMonitoringView], error) {
	return infrastructureUseCase.Page[infrastructureUseCase.CapacityMonitoringView]{}, nil
}
func (f *fakeUseCase) ListStands(_ context.Context, filter infrastructureUseCase.StandsFilter) (infrastructureUseCase.StandsPage, error) {
	f.standsFilter = filter
	return infrastructureUseCase.StandsPage{
		Items: []infrastructureUseCase.PlatformStandView{{EventName: "CTF", EventTag: "ctf", TeamName: "Red", Status: eventStandModel.StatusFailed, Reason: "boom", Generation: 3, Resources: infrastructureUseCase.ResourcesView{Known: true, Available: true, CPUMillicores: 250, MemoryBytes: 1024}}},
		Total: 1, Page: filter.Page, PageSize: filter.PageSize,
	}, nil
}
func (f *fakeUseCase) ListStandEvents(context.Context) ([]infrastructureUseCase.StandEventView, error) {
	return nil, nil
}
func (f *fakeUseCase) InfrastructureSummary(context.Context) (infrastructureUseCase.SummaryView, error) {
	cpu := 42.5
	return infrastructureUseCase.SummaryView{
		Stands:     infrastructureUseCase.StandCountsView{Failed: 2, Active: 5},
		Moderators: infrastructureUseCase.StandCountsView{Active: 1},
		TestLabs:   infrastructureUseCase.TestLabCountsView{Total: 4, Active: 3, Expired: 1},
		Capacity:   infrastructureUseCase.CapacityUsageView{Available: true, CPUPercent: &cpu},
	}, nil
}
func (f *fakeUseCase) RecreateTeamStand(_ context.Context, eventID, teamID, by uuid.UUID) (eventUseCase.StandTeamView, error) {
	f.recreated = [3]uuid.UUID{eventID, teamID, by}
	return eventUseCase.StandTeamView{TeamID: teamID, Status: eventStandModel.StatusCreating, Generation: 4}, f.recreateErr
}

func (f *fakeUseCase) ListTestLabs(_ context.Context, search string, page, pageSize int) (infrastructureUseCase.TestLabsPage, error) {
	f.testLabsQuery = [3]any{search, page, pageSize}
	return infrastructureUseCase.TestLabsPage{
		Items: []infrastructureUseCase.TestLabView{{ExerciseName: "Web 1", AuthorName: "Ann", AuthorEmail: "ann@example.test", VariantNumber: 2, Status: infrastructureUseCase.TestLabReady, Expired: true, Resources: infrastructureUseCase.ResourcesView{Known: true, Available: true, CPUMillicores: 300, MemoryBytes: 2048}}},
		Total: 1, Page: page, PageSize: pageSize,
	}, nil
}
func (f *fakeUseCase) TerminateTestLab(_ context.Context, id uuid.UUID) error {
	f.terminated = id
	return f.terminateErr
}

func (f *fakeUseCase) GetTeamStandDetail(_ context.Context, _, teamID uuid.UUID) (eventUseCase.StandDetailView, error) {
	live := exerciseModel.LabDeployStatus{
		Phase: exerciseModel.DeployPhaseQueued, ImageWarning: "web:latest",
		Queue:   &exerciseModel.LabQueue{Position: 2, Length: 5, Reason: exerciseModel.QueueReasonInFlightLimit},
		Devices: []exerciseModel.LabDeployedDevice{{Name: "web", Snapshot: &exerciseModel.DeviceSnapshot{SizeBytes: 7, Rescue: true}}},
	}
	return eventUseCase.StandDetailView{TeamID: teamID, Status: eventStandModel.StatusCreating, Labs: []eventUseCase.StandLabDetailView{{ChallengeName: "Web", Live: &live}}}, nil
}
func (f *fakeUseCase) ResetStandDevice(_ context.Context, eventID, teamID, challengeID uuid.UUID, device string) error {
	f.deviceCall = "reset " + eventID.String() + " " + teamID.String() + " " + challengeID.String() + " " + device
	return f.deviceErr
}
func (f *fakeUseCase) RescueStandDevice(_ context.Context, _, _, _ uuid.UUID, device string, enable bool) error {
	f.deviceCall = "rescue " + device + " " + strconv.FormatBool(enable)
	return f.deviceErr
}
func (f *fakeUseCase) GetTestLabDetail(_ context.Context, id uuid.UUID) (infrastructureUseCase.TestLabDetail, error) {
	return infrastructureUseCase.TestLabDetail{ID: id, Status: infrastructureUseCase.TestLabQueued, Live: exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseQueued, GroupImageWarning: "vpn:1"}}, nil
}
func (f *fakeUseCase) ResetTestLabDevice(_ context.Context, _ uuid.UUID, device string) error {
	f.deviceCall = "test-reset " + device
	return f.deviceErr
}
func (f *fakeUseCase) RescueTestLabDevice(_ context.Context, _ uuid.UUID, device string, enable bool) error {
	f.deviceCall = "test-rescue " + device + " " + strconv.FormatBool(enable)
	return f.deviceErr
}

// recordingProtection notes which permission gates which route and lets every
// request through with a fixed super-admin identity.
type recordingProtection struct {
	gates map[string]rbac.Permission
	actor uuid.UUID
	seen  *string
}

func (p *recordingProtection) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		p.gates[ctx.Request.Method+" "+ctx.FullPath()] = required
		ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), rbac.Claims{UserID: p.actor, Role: rbac.RoleSuperAdmin}))
		ctx.Next()
		if p.seen != nil {
			*p.seen = audit.Target(ctx)
		}
	}
}

func newRouter(uc *fakeUseCase, prot *recordingProtection) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewInfrastructureAPIHandler(uc, prot).Init(router.Group("api"))
	return router
}

func newProtection() *recordingProtection {
	return &recordingProtection{gates: map[string]rbac.Permission{}, actor: uuid.Must(uuid.NewV7())}
}

func do(router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, path, nil))
	return recorder
}

func data(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct{ Data map[string]any }
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body.String(), err)
	}
	return envelope.Data
}

func TestRoutesAreGatedByReadExceptRecreateWhichNeedsWrite(t *testing.T) {
	prot := newProtection()
	router := newRouter(&fakeUseCase{}, prot)
	for _, path := range []string{"/api/infrastructure/status", "/api/infrastructure/summary", "/api/infrastructure/monitoring/current", "/api/infrastructure/stands", "/api/infrastructure/stands/events", "/api/infrastructure/test-labs"} {
		do(router, http.MethodGet, path)
	}
	do(router, http.MethodPost, "/api/infrastructure/stands/"+uuid.Must(uuid.NewV7()).String()+"/"+uuid.Must(uuid.NewV7()).String()+"/recreate")
	do(router, http.MethodPost, "/api/infrastructure/test-labs/"+uuid.Must(uuid.NewV7()).String()+"/terminate")
	for route, perm := range prot.gates {
		want := rbac.PermInfrastructureRead
		if strings.HasPrefix(route, "POST ") {
			want = rbac.PermInfrastructureWrite
		}
		if perm != want {
			t.Errorf("%s gated by %q, want %q", route, perm, want)
		}
	}
	if len(prot.gates) != 8 {
		t.Fatalf("gated routes = %v, want 8", prot.gates)
	}
}

func TestStatusUsesPascalCase(t *testing.T) {
	recorder := do(newRouter(&fakeUseCase{}, newProtection()), http.MethodGet, "/api/infrastructure/status")
	body := recorder.Body.String()
	for _, key := range []string{`"Available":true`, `"Healthy":true`, `"Mode":"available"`, `"Agents":[]`, `"Capabilities":{"Laboratories":true}`} {
		if !strings.Contains(body, key) {
			t.Fatalf("status body lacks %s: %s", key, body)
		}
	}
}

func TestCurrentMonitoringDefaultsToActiveEventsAndBlanksModeratorsTeam(t *testing.T) {
	uc := &fakeUseCase{}
	router := newRouter(uc, newProtection())

	recorder := do(router, http.MethodGet, "/api/infrastructure/monitoring/current")
	if uc.includeRecent == nil || *uc.includeRecent {
		t.Fatalf("includeRecent = %v, want false by default", uc.includeRecent)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"EventName":"CTF"`) || !strings.Contains(body, `"TeamName":""`) || !strings.Contains(body, `"Moderators":true`) || !strings.Contains(body, `"Payload":{"labs":[{"name":"web"}]}`) {
		t.Fatalf("unexpected current body: %s", body)
	}

	do(router, http.MethodGet, "/api/infrastructure/monitoring/current?includeRecent=true")
	if !*uc.includeRecent {
		t.Fatal("includeRecent=true not forwarded")
	}
	if got := do(router, http.MethodGet, "/api/infrastructure/monitoring/current?includeRecent=maybe").Code; got != http.StatusBadRequest {
		t.Fatalf("bad includeRecent status = %d, want 400", got)
	}
}

func TestObservationsReturnACursorPage(t *testing.T) {
	next := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{observations: infrastructureUseCase.Page[infrastructureUseCase.LabMonitoringView]{
		Items:   []infrastructureUseCase.LabMonitoringView{{ID: next, EventName: "CTF", Payload: json.RawMessage(`{}`)}},
		HasMore: true, NextCursor: &next,
	}}
	router := newRouter(uc, newProtection())

	recorder := do(router, http.MethodGet, "/api/infrastructure/monitoring/observations?pageSize=25")
	got := data(t, recorder)
	if got["HasMore"] != true || got["NextCursor"] != next.String() {
		t.Fatalf("page = %v, want HasMore and NextCursor", got)
	}
	if items, _ := got["Items"].([]any); len(items) != 1 {
		t.Fatalf("items = %v", got["Items"])
	}
	if uc.pageSize != 25 {
		t.Fatalf("pageSize = %d, want 25", uc.pageSize)
	}

	uc.observations = infrastructureUseCase.Page[infrastructureUseCase.LabMonitoringView]{}
	last := data(t, do(router, http.MethodGet, "/api/infrastructure/monitoring/observations"))
	if _, has := last["NextCursor"]; has || last["HasMore"] != false {
		t.Fatalf("last page = %v, want no NextCursor", last)
	}
	if uc.pageSize != observationsDefaultPageSize {
		t.Fatalf("default pageSize = %d", uc.pageSize)
	}
	for _, bad := range []string{"pageSize=0", "pageSize=9999", "pageSize=x", "cursor=nope", "from=yesterday", "eventId=zz"} {
		if code := do(router, http.MethodGet, "/api/infrastructure/monitoring/observations?"+bad).Code; code != http.StatusBadRequest {
			t.Errorf("%s -> %d, want 400", bad, code)
		}
	}
}

func TestStandsFiltersAndPaging(t *testing.T) {
	uc := &fakeUseCase{}
	router := newRouter(uc, newProtection())
	eventID := uuid.Must(uuid.NewV7())

	recorder := do(router, http.MethodGet, "/api/infrastructure/stands?eventId="+eventID.String()+"&status=active&search=%20red%20&page=2&pageSize=25")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
	f := uc.standsFilter
	if f.EventID.UUID != eventID || !f.EventID.Valid || f.Search != "red" || f.Page != 2 || f.PageSize != 25 ||
		len(f.Statuses) != 2 || f.Statuses[0] != eventStandModel.StatusCreating || f.Statuses[1] != eventStandModel.StatusReady {
		t.Fatalf("filter = %+v", f)
	}
	body := recorder.Body.String()
	for _, want := range []string{`"EventTag":"ctf"`, `"Status":"failed"`, `"Reason":"boom"`, `"Generation":3`, `"CPUMillicores":250`, `"MemoryBytes":1024`, `"UsageAvailable":true`, `"Total":1`, `"Page":2`} {
		if !strings.Contains(body, want) {
			t.Errorf("stands body lacks %s: %s", want, body)
		}
	}

	do(router, http.MethodGet, "/api/infrastructure/stands")
	if uc.standsFilter.Page != 1 || uc.standsFilter.PageSize != 20 || len(uc.standsFilter.Statuses) != 0 {
		t.Fatalf("defaults = %+v", uc.standsFilter)
	}
	do(router, http.MethodGet, "/api/infrastructure/stands?kind=moderators")
	if uc.standsFilter.Kind != "moderators" {
		t.Fatalf("kind = %q, want moderators", uc.standsFilter.Kind)
	}
	for _, bad := range []string{"kind=other", "status=broken", "eventId=nope", "page=-1", "pageSize=100000"} {
		if code := do(router, http.MethodGet, "/api/infrastructure/stands?"+bad).Code; code != http.StatusBadRequest {
			t.Errorf("%s -> %d, want 400", bad, code)
		}
	}
}

func TestSummaryIsPascalCase(t *testing.T) {
	body := do(newRouter(&fakeUseCase{}, newProtection()), http.MethodGet, "/api/infrastructure/summary").Body.String()
	for _, want := range []string{`"Failed":2`, `"Active":5`, `"CPUPercent":42.5`, `"MemoryPercent":null`, `"Available":true`, `"Moderators":{"Total":0,"Creating":0,"Ready":0,"Failed":0,"Removed":0,"Active":1}`, `"TestLabs":{"Total":4,"Active":3,"Expired":1}`} {
		if !strings.Contains(body, want) {
			t.Errorf("summary lacks %s: %s", want, body)
		}
	}
}

func TestRecreateUsesRouteIdsActorAndRecordsTheAuditTarget(t *testing.T) {
	uc := &fakeUseCase{}
	prot := newProtection()
	var target string
	prot.seen = &target
	router := newRouter(uc, prot)
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	recorder := do(router, http.MethodPost, "/api/infrastructure/stands/"+eventID.String()+"/"+teamID.String()+"/recreate")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
	if uc.recreated != [3]uuid.UUID{eventID, teamID, prot.actor} {
		t.Fatalf("use case got %v, want event, team and the acting admin", uc.recreated)
	}
	if want := "event:" + eventID.String() + " team:" + teamID.String(); target != want {
		t.Fatalf("audit target = %q, want %q", target, want)
	}
	if body := recorder.Body.String(); !strings.Contains(body, `"Status":"creating"`) || !strings.Contains(body, `"Generation":4`) {
		t.Fatalf("recreate body: %s", body)
	}
	if code := do(router, http.MethodPost, "/api/infrastructure/stands/nope/"+teamID.String()+"/recreate").Code; code != http.StatusBadRequest {
		t.Fatalf("bad event id -> %d, want 400", code)
	}
}

func TestRecreateFailureIsNotRecordedAsSuccess(t *testing.T) {
	uc := &fakeUseCase{recreateErr: eventStandModel.ErrStandNotDeployed.Err()}
	router := newRouter(uc, newProtection())
	recorder := do(router, http.MethodPost, "/api/infrastructure/stands/"+uuid.Must(uuid.NewV7()).String()+"/"+uuid.Must(uuid.NewV7()).String()+"/recreate")
	if recorder.Code < http.StatusBadRequest {
		t.Fatalf("status %d, want an error status so the audit middleware skips it", recorder.Code)
	}
}

func TestTestLabsListFiltersAndShape(t *testing.T) {
	uc := &fakeUseCase{}
	router := newRouter(uc, newProtection())
	recorder := do(router, http.MethodGet, "/api/infrastructure/test-labs?search=%20web%20&page=2&pageSize=25")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
	if uc.testLabsQuery != [3]any{"web", 2, 25} {
		t.Fatalf("query = %v", uc.testLabsQuery)
	}
	body := recorder.Body.String()
	for _, want := range []string{`"ExerciseName":"Web 1"`, `"AuthorEmail":"ann@example.test"`, `"VariantNumber":2`, `"Status":"ready"`, `"Expired":true`, `"CPUMillicores":300`, `"MemoryBytes":2048`, `"Total":1`} {
		if !strings.Contains(body, want) {
			t.Errorf("test labs body lacks %s: %s", want, body)
		}
	}
	if code := do(router, http.MethodGet, "/api/infrastructure/test-labs?page=-1").Code; code != http.StatusBadRequest {
		t.Errorf("bad page -> %d, want 400", code)
	}
}

func TestTerminateTestLabUsesRouteIdAndRecordsTheAuditTarget(t *testing.T) {
	uc := &fakeUseCase{}
	prot := newProtection()
	var target string
	prot.seen = &target
	router := newRouter(uc, prot)
	id := uuid.Must(uuid.NewV7())
	if code := do(router, http.MethodPost, "/api/infrastructure/test-labs/"+id.String()+"/terminate").Code; code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	if uc.terminated != id || target != "test-lab:"+id.String() {
		t.Fatalf("terminated %v, audit target %q", uc.terminated, target)
	}
	if code := do(router, http.MethodPost, "/api/infrastructure/test-labs/nope/terminate").Code; code != http.StatusBadRequest {
		t.Fatalf("bad id -> %d, want 400", code)
	}
	uc.terminateErr = infraModel.ErrTestLabNotFound.Err()
	if code := do(router, http.MethodPost, "/api/infrastructure/test-labs/"+id.String()+"/terminate").Code; code != http.StatusNotFound {
		t.Fatalf("gone lab -> %d, want 404", code)
	}
}

func TestDeviceActionsAreWriteGatedAndRecordTheAuditTarget(t *testing.T) {
	uc := &fakeUseCase{}
	prot := newProtection()
	var target string
	prot.seen = &target
	router := newRouter(uc, prot)
	eventID, teamID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	base := "/api/infrastructure/stands/" + eventID.String() + "/" + teamID.String() + "/challenges/" + challengeID.String() + "/devices/web"

	if code := do(router, http.MethodPost, base+"/reset").Code; code != http.StatusOK {
		t.Fatalf("reset status %d", code)
	}
	if want := "reset " + eventID.String() + " " + teamID.String() + " " + challengeID.String() + " web"; uc.deviceCall != want {
		t.Fatalf("call %q, want %q", uc.deviceCall, want)
	}
	if want := "event:" + eventID.String() + " team:" + teamID.String() + " challenge:" + challengeID.String() + " device:web"; target != want {
		t.Fatalf("audit target %q, want %q", target, want)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, base+"/rescue", strings.NewReader(`{"Enable":true}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || uc.deviceCall != "rescue web true" {
		t.Fatalf("rescue status %d call %q", recorder.Code, uc.deviceCall)
	}
	if code := do(router, http.MethodPost, "/api/infrastructure/test-labs/"+teamID.String()+"/devices/db/reset").Code; code != http.StatusOK || uc.deviceCall != "test-reset db" {
		t.Fatalf("test lab reset status %d call %q", code, uc.deviceCall)
	}
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/api/infrastructure/test-labs/"+teamID.String()+"/devices/db/rescue", strings.NewReader(`{"Enable":false}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || uc.deviceCall != "test-rescue db false" {
		t.Fatalf("test lab rescue status %d call %q", recorder.Code, uc.deviceCall)
	}
	for _, route := range []string{
		"POST /api/infrastructure/stands/:eventID/:teamID/challenges/:challengeID/devices/:device/reset",
		"POST /api/infrastructure/stands/:eventID/:teamID/challenges/:challengeID/devices/:device/rescue",
		"POST /api/infrastructure/test-labs/:labID/devices/:device/reset",
		"POST /api/infrastructure/test-labs/:labID/devices/:device/rescue",
	} {
		if prot.gates[route] != rbac.PermInfrastructureWrite {
			t.Errorf("%s gated by %q, want infrastructure.write", route, prot.gates[route])
		}
	}
}

func TestDeviceActionErrorsMapToTheirHTTPStatuses(t *testing.T) {
	uc := &fakeUseCase{}
	router := newRouter(uc, newProtection())
	path := "/api/infrastructure/test-labs/" + uuid.Must(uuid.NewV7()).String() + "/devices/web/reset"
	for _, c := range []struct {
		err  error
		code int
	}{
		{infraModel.ErrDeviceNotPersistent.Err(), http.StatusConflict},
		{infraModel.ErrDeviceNotFound.Err(), http.StatusNotFound},
		{infraModel.ErrDeviceActionRetry.Err(), http.StatusServiceUnavailable},
	} {
		uc.deviceErr = c.err
		if code := do(router, http.MethodPost, path).Code; code != c.code {
			t.Errorf("%v -> %d, want %d", c.err, code, c.code)
		}
	}
}

func TestStandAndTestLabDetailExposeQueueSnapshotAndWarnings(t *testing.T) {
	router := newRouter(&fakeUseCase{}, newProtection())
	body := do(router, http.MethodGet, "/api/infrastructure/stands/"+uuid.Must(uuid.NewV7()).String()+"/"+uuid.Must(uuid.NewV7()).String()+"/detail").Body.String()
	for _, want := range []string{`"Phase":"Queued"`, `"Queue":{"Position":2,"Length":5,"Reason":"InFlightLimit","Message":"","Pods":0,"Pending":0}`, `"ImageWarning":"web:latest"`, `"SizeBytes":7`, `"Rescue":true`, `"ChallengeName":"Web"`} {
		if !strings.Contains(body, want) {
			t.Errorf("stand detail lacks %s: %s", want, body)
		}
	}
	body = do(router, http.MethodGet, "/api/infrastructure/test-labs/"+uuid.Must(uuid.NewV7()).String()+"/detail").Body.String()
	for _, want := range []string{`"Status":"queued"`, `"GroupImageWarning":"vpn:1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("test lab detail lacks %s: %s", want, body)
		}
	}
}

func TestAgentsListExposesStateButNeverSecrets(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	expires := time.Date(2027, 1, 2, 3, 4, 5, 0, time.UTC)
	uc := &fakeUseCase{agentsView: infrastructureUseCase.AgentsView{EnvironmentUsed: false, Items: []infrastructureUseCase.AgentAdminView{{
		AgentRegistration: infraModel.AgentRegistration{ID: id, Name: "eu", Source: infraModel.AgentSourceAdmin, Endpoint: "eu.example.com:443", Enabled: true, Priority: 5, HasCA: true, Tenant: "platform", AccessKeyID: "k-1", CertNotAfter: &expires},
		InUse:             true, Groups: 12, RetiredKeys: 1, Probe: agentfleet.AgentProbe{Connected: true, Healthy: true, Latency: 42 * time.Millisecond},
	}}}}
	prot := newProtection()
	router := newRouter(uc, prot)
	recorder := do(router, http.MethodGet, "/api/infrastructure/agents")
	body := recorder.Body.String()
	for _, want := range []string{`"EnvironmentUsed":false`, `"Name":"eu"`, `"Endpoint":"eu.example.com:443"`, `"Priority":5`, `"InUse":true`, `"Groups":12`, `"Healthy":true`, `"LatencyMs":42`, `"Tenant":"platform"`, `"AccessKeyID":"k-1"`, `"RetiredKeys":1`, `"CertExpiresAt":"2027-01-02T03:04:05Z"`, `"HasCA":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %s: %s", want, body)
		}
	}
	for _, secret := range []string{"Ciphertext", "ClientKey", "ClientCert", "PrivateKey", "EnrollmentToken"} {
		if strings.Contains(body, secret) {
			t.Errorf("the list must never carry connection secrets (%s): %s", secret, body)
		}
	}
	if prot.gates["GET /api/infrastructure/agents"] != rbac.PermInfrastructureRead {
		t.Errorf("list gate = %q", prot.gates["GET /api/infrastructure/agents"])
	}
}

func TestAgentWritesAreGatedMapTheFormAndAuditOnlyTheAgentID(t *testing.T) {
	uc := &fakeUseCase{}
	prot := newProtection()
	var target string
	prot.seen = &target
	router := newRouter(uc, prot)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		return recorder
	}
	created := send(http.MethodPost, "/api/infrastructure/agents", `{"Name":"eu","Endpoint":"eu:443","EnrollmentToken":"tok","CAPEM":"A","Enabled":true,"Priority":7}`)
	if created.Code != http.StatusOK || uc.enrollInput != (infraModel.AgentEnrollment{Name: "eu", Endpoint: "eu:443", Token: "tok", CAPEM: "A", Enabled: true, Priority: 7}) {
		t.Fatalf("enroll: %d %+v", created.Code, uc.enrollInput)
	}
	if strings.Contains(created.Body.String(), "tok") || !strings.Contains(created.Body.String(), `"Tenant":"platform"`) {
		t.Fatalf("the token must never be echoed: %s", created.Body.String())
	}
	if !strings.HasPrefix(target, "agent:") || strings.Contains(target, "tok") {
		t.Fatalf("audit target = %q", target)
	}
	id := uuid.Must(uuid.NewV7())
	if code := send(http.MethodPut, "/api/infrastructure/agents/"+id.String(), `{"Name":"eu2","CAPEM":"","Enabled":false,"Priority":1}`).Code; code != http.StatusOK || uc.agentID != id || uc.updateInput.Name != "eu2" || uc.updateInput.Priority != 1 {
		t.Fatalf("update: %d id=%v input=%+v", code, uc.agentID, uc.updateInput)
	}
	if target != "agent:"+id.String() {
		t.Fatalf("audit target = %q", target)
	}
	if code := send(http.MethodDelete, "/api/infrastructure/agents/"+id.String(), "").Code; code != http.StatusOK {
		t.Fatalf("delete: %d", code)
	}
	if code := send(http.MethodPost, "/api/infrastructure/agents/"+id.String()+"/renew-certificate", "").Code; code != http.StatusOK || uc.deviceCall != "renew" {
		t.Fatalf("renew: %d %q", code, uc.deviceCall)
	}
	if rotated := send(http.MethodPost, "/api/infrastructure/agents/"+id.String()+"/rotate-access-key", ""); rotated.Code != http.StatusOK || !strings.Contains(rotated.Body.String(), `"AccessKeyID":"k-new"`) {
		t.Fatalf("rotate: %d %s", rotated.Code, rotated.Body.String())
	}
	if code := send(http.MethodPost, "/api/infrastructure/agents", `{"Name":"eu","Endpoint":"eu:443"`).Code; code != http.StatusBadRequest {
		t.Fatalf("bad json -> %d", code)
	}
	checked := send(http.MethodPost, "/api/infrastructure/agents/"+id.String()+"/check", "")
	if checked.Code != http.StatusOK || !strings.Contains(checked.Body.String(), `"Healthy":true`) || !strings.Contains(checked.Body.String(), `"LatencyMs":1`) {
		t.Fatalf("check: %d %s", checked.Code, checked.Body.String())
	}
	if code := send(http.MethodPut, "/api/infrastructure/agents/nope", `{}`).Code; code != http.StatusBadRequest {
		t.Fatalf("bad id -> %d", code)
	}
	for route, want := range map[string]rbac.Permission{
		"POST /api/infrastructure/agents":                rbac.PermInfrastructureWrite,
		"PUT /api/infrastructure/agents/:agentID":        rbac.PermInfrastructureWrite,
		"DELETE /api/infrastructure/agents/:agentID":     rbac.PermInfrastructureWrite,
		"POST /api/infrastructure/agents/:agentID/check": rbac.PermInfrastructureRead,
	} {
		if prot.gates[route] != want {
			t.Errorf("%s gated by %q, want %q", route, prot.gates[route], want)
		}
	}
	uc.agentErr = infraModel.ErrAgentInUse.Err()
	if code := send(http.MethodDelete, "/api/infrastructure/agents/"+id.String(), "").Code; code != http.StatusConflict {
		t.Fatalf("in use -> %d, want 409", code)
	}
	uc.agentErr = infraModel.ErrAgentNotFound.Err()
	if code := send(http.MethodPost, "/api/infrastructure/agents/"+id.String()+"/check", "").Code; code != http.StatusNotFound {
		t.Fatalf("unknown agent -> %d, want 404", code)
	}
}

func TestAgentCapacityArchiveAndDeleteFlow(t *testing.T) {
	seen := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	cpu := int64(8000)
	id := uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{agentsView: infrastructureUseCase.AgentsView{Items: []infrastructureUseCase.AgentAdminView{
		{AgentRegistration: infraModel.AgentRegistration{ID: id, Name: "eu", CapacityCPUMillicores: &cpu, CapacitySeenAt: &seen}},
		{AgentRegistration: infraModel.AgentRegistration{ID: uuid.Must(uuid.NewV7()), Name: "new"}},
	}}, preview: infrastructureUseCase.DeletePreview{RunningGroups: 2, FutureReservations: []infrastructureUseCase.ReservationImpact{{EventName: "CTF", CPUMillicores: 4000, StartsAt: seen}}}}
	prot := newProtection()
	router := newRouter(uc, prot)
	body := do(router, http.MethodGet, "/api/infrastructure/agents?archived=1").Body.String()
	for _, want := range []string{`"Capacity":{"CPUMillicores":8000,"MemoryBytes":null,"SeenAt":"2026-10-01T12:00:00Z"}`, `"Capacity":{"CPUMillicores":null,"MemoryBytes":null,"SeenAt":null}`, `"ArchivedAt":null`} {
		if !strings.Contains(body, want) {
			t.Errorf("list lacks %s: %s", want, body)
		}
	}
	if !uc.listArchived {
		t.Error("archived=1 must list archived agents")
	}
	preview := do(router, http.MethodGet, "/api/infrastructure/agents/"+id.String()+"/delete-preview").Body.String()
	for _, want := range []string{`"RunningGroups":2`, `"EventName":"CTF"`, `"CPUMillicores":4000`} {
		if !strings.Contains(preview, want) {
			t.Errorf("preview lacks %s: %s", want, preview)
		}
	}
	if code := do(router, http.MethodDelete, "/api/infrastructure/agents/"+id.String()).Code; code != http.StatusOK || uc.confirm {
		t.Fatalf("delete without confirm: %d confirm=%v", code, uc.confirm)
	}
	if code := do(router, http.MethodDelete, "/api/infrastructure/agents/"+id.String()+"?confirm=1").Code; code != http.StatusOK || !uc.confirm {
		t.Fatalf("delete with confirm: %d confirm=%v", code, uc.confirm)
	}
	uc.agentErr = infraModel.ErrAgentDeleteNeedsConfirm.Err()
	if code := do(router, http.MethodDelete, "/api/infrastructure/agents/"+id.String()).Code; code != http.StatusConflict {
		t.Fatalf("needs confirm -> %d, want 409", code)
	}
	uc.agentErr = nil
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/infrastructure/agents/"+id.String()+"/reconnect", strings.NewReader(`{"EnrollmentToken":"tok","CAPEM":"ca"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || uc.reconnect != "tok|ca" || strings.Contains(recorder.Body.String(), "tok") {
		t.Fatalf("reconnect: %d %q %s", recorder.Code, uc.reconnect, recorder.Body.String())
	}
	if prot.gates["POST /api/infrastructure/agents/:agentID/reconnect"] != rbac.PermInfrastructureWrite || prot.gates["GET /api/infrastructure/agents/:agentID/delete-preview"] != rbac.PermInfrastructureRead {
		t.Fatalf("gates = %v", prot.gates)
	}
}
