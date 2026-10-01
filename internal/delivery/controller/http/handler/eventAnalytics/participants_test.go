package eventAnalytics

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
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// peopleUseCase serves the «Учасники» and «Комунікації» routes; any other
// use-case call panics on the nil embedded interface.
type peopleUseCase struct {
	IUseCase
	levels         []eventAnalyticsUseCase.Level
	participants   eventAnalyticsUseCase.ParticipantsView
	communications eventAnalyticsUseCase.CommunicationsView
	from, to       *time.Time
}

func (f *peopleUseCase) RequireEventAnalytics(_ context.Context, _ uuid.UUID, _ rbac.Claims, level eventAnalyticsUseCase.Level) error {
	f.levels = append(f.levels, level)
	return nil
}
func (f *peopleUseCase) GetEventAnalyticsParticipants(_ context.Context, _ uuid.UUID, from, to *time.Time) (eventAnalyticsUseCase.ParticipantsView, error) {
	f.from, f.to = from, to
	return f.participants, nil
}
func (f *peopleUseCase) GetEventAnalyticsCommunications(context.Context, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.CommunicationsView, error) {
	return f.communications, nil
}

func servePeople(t *testing.T, uc *peopleUseCase, path string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleUser}))
	}, response.WithErrorHandler)
	NewEventAnalyticsAPIHandler(uc, allowAll{}).Init(router.Group("api"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/"+uuid.Must(uuid.NewV7()).String()+"/manage/analytics/"+path, nil))
	return w
}

func TestParticipantsReportNeedsSectionsOnlyAndPassesTheWindow(t *testing.T) {
	uc := &peopleUseCase{participants: eventAnalyticsUseCase.ParticipantsView{
		TeamMode: true,
		Funnel:   []eventAnalyticsUseCase.FunnelStageView{{Stage: eventAnalyticsUseCase.StageInvited, Count: 3}},
		Answers:  eventAnalyticsUseCase.AnswersView{Questions: []eventAnalyticsUseCase.QuestionView{{Key: "city", Input: "select"}}},
	}}
	w := servePeople(t, uc, "participants?from=2026-09-01T00:00:00Z")
	if w.Code != http.StatusOK || len(uc.levels) != 1 || uc.levels[0] != eventAnalyticsUseCase.LevelSections {
		t.Fatalf("status = %d levels = %v", w.Code, uc.levels)
	}
	if uc.from == nil || uc.to != nil {
		t.Fatalf("window: %v %v", uc.from, uc.to)
	}
	var body struct {
		Data participantsResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Data.TeamMode || len(body.Data.Funnel) != 1 || body.Data.Funnel[0].Stage != "invited" || len(body.Data.Answers.Questions) != 1 {
		t.Fatalf("body: %+v", body.Data)
	}
	if !strings.Contains(w.Body.String(), `"Buckets":[]`) {
		t.Fatalf("empty lists must be arrays: %s", w.Body.String())
	}
}

func TestParticipantsExportWritesOneTableAsCSV(t *testing.T) {
	uc := &peopleUseCase{participants: eventAnalyticsUseCase.ParticipantsView{
		Funnel: []eventAnalyticsUseCase.FunnelStageView{{Stage: eventAnalyticsUseCase.StageApproved, Count: 7}},
		DropOff: eventAnalyticsUseCase.DropOffView{Rows: []eventAnalyticsUseCase.DropOffRowView{
			{Name: "=HYPERLINK(1)", Email: "a@b.c", RegisteredAt: time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC), OpenedTasks: 2},
		}},
	}}
	w := servePeople(t, uc, "participants/export.csv?table=funnel")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("status = %d type = %s", w.Code, w.Header().Get("Content-Type"))
	}
	if !strings.Contains(w.Body.String(), "Схвалено,7") {
		t.Fatalf("funnel csv: %q", w.Body.String())
	}
	w = servePeople(t, uc, "participants/export.csv?table=dropoff")
	if !strings.Contains(w.Body.String(), "'=HYPERLINK(1),a@b.c") || !strings.Contains(w.Body.String(), "2026-09-01T10:00:00Z") {
		t.Fatalf("dropoff csv must neutralize formulas: %q", w.Body.String())
	}
	if w = servePeople(t, uc, "participants/export.csv?table=secrets"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown table = %d, want 400", w.Code)
	}
	if w = servePeople(t, uc, "participants/export.csv"); w.Code != http.StatusBadRequest {
		t.Fatalf("no table = %d, want 400", w.Code)
	}
}

func TestCommunicationsReportAndExport(t *testing.T) {
	rate := 0.5
	uc := &peopleUseCase{communications: eventAnalyticsUseCase.CommunicationsView{
		Totals: eventAnalyticsUseCase.CommsTypeView{EmailSent: 3, ReadRate: &rate},
		Types:  []eventAnalyticsUseCase.CommsTypeView{{Type: "event.start", EmailSent: 3, EmailErrors: 1, InAppCreated: 2, InAppRead: 1, ReadRate: &rate}},
		Forms:  []eventAnalyticsUseCase.FormCompletionView{{Title: "Feedback", Assigned: 4, Completed: 1}},
	}}
	w := servePeople(t, uc, "communications")
	var body struct {
		Data communicationsResponse `json:"data"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Data.Totals.EmailSent != 3 || len(body.Data.Types) != 1 || *body.Data.Types[0].ReadRate != 0.5 || body.Data.Forms[0].CompletionRate != nil {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	w = servePeople(t, uc, "communications/export.csv?table=types")
	if !strings.Contains(w.Body.String(), "event.start,3,1,0,0,2,1,0.500") {
		t.Fatalf("types csv: %q", w.Body.String())
	}
	w = servePeople(t, uc, "communications/export.csv?table=forms")
	if !strings.Contains(w.Body.String(), "Feedback,4,1,0,") {
		t.Fatalf("forms csv: %q", w.Body.String())
	}
	if w = servePeople(t, uc, "communications/export.csv?table=x"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown table = %d, want 400", w.Code)
	}
}
