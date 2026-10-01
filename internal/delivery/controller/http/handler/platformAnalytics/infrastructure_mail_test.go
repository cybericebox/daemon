package platformAnalytics

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
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
)

// imUseCase serves the infrastructure and mail reads; every other section
// panics on the nil embedded interface.
type imUseCase struct {
	IUseCase

	infra platformAnalyticsUseCase.InfrastructureView
	mail  platformAnalyticsUseCase.MailView

	from, to     *time.Time
	channel      string
	transport    string
	kind         string
	includeTests bool
	calls        int
}

func (u *imUseCase) GetInfrastructure(_ context.Context, from, to *time.Time) (platformAnalyticsUseCase.InfrastructureView, error) {
	u.calls++
	u.from, u.to = from, to
	return u.infra, nil
}

func (u *imUseCase) GetMail(_ context.Context, from, to *time.Time, channel, transport, kind string, includeTests bool) (platformAnalyticsUseCase.MailView, error) {
	u.calls++
	u.from, u.to = from, to
	u.channel, u.transport, u.kind, u.includeTests = channel, transport, kind, includeTests
	return u.mail, nil
}

// imProtection admits only the permissions in allow; a denied caller gets 403.
type imProtection struct {
	allow map[rbac.Permission]bool
	asked []rbac.Permission
}

func (p *imProtection) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		p.asked = append(p.asked, required)
		if !p.allow[required] {
			response.AbortWithForbidden(ctx)
		}
	}
}

func imServe(t *testing.T, uc *imUseCase, prot *imProtection, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewPlatformAnalyticsAPIHandler(uc, prot).Init(router.Group("api"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

func imStaff() *imProtection {
	return &imProtection{allow: map[rbac.Permission]bool{rbac.PermAnalyticsRead: true}}
}

var imDay = time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)

func TestInfrastructureAndMailRoutesNeedAnalyticsRead(t *testing.T) {
	for _, target := range []string{
		"/api/analytics/infrastructure",
		"/api/analytics/infrastructure/export.csv?table=peaks",
		"/api/analytics/mail",
		"/api/analytics/mail/export.csv?table=daily",
	} {
		uc := &imUseCase{}
		prot := &imProtection{allow: map[rbac.Permission]bool{}}
		w := imServe(t, uc, prot, target)
		if w.Code != http.StatusForbidden || uc.calls != 0 {
			t.Fatalf("%s: status = %d calls = %d, want 403 and no read", target, w.Code, uc.calls)
		}
		if len(prot.asked) != 1 || prot.asked[0] != rbac.PermAnalyticsRead {
			t.Fatalf("%s: permissions asked = %v", target, prot.asked)
		}
	}
}

func TestInfrastructureMapsTheReportAndPassesThePeriod(t *testing.T) {
	uc := &imUseCase{infra: platformAnalyticsUseCase.InfrastructureView{
		Period: platformAnalyticsModel.Period{From: imDay, To: imDay.Add(24 * time.Hour)},
		Bucket: "hour",
		Stands: platformAnalyticsUseCase.StandCountsView{Active: 7, Failed: 1},
		StandHours: platformAnalyticsUseCase.StandHoursView{TotalHours: 12.5, TotalEvents: 2, Events: []platformAnalyticsUseCase.StandHoursEventView{
			{EventID: uuid.Must(uuid.NewV7()), EventName: "Cup", Hours: 10, Stands: 4},
		}},
		Peaks:    []platformAnalyticsUseCase.PeakPointView{{At: imDay, Peak: 3}},
		PeakMax:  3,
		Failures: []platformAnalyticsUseCase.FailureReasonView{{Code: "image_pull", Labs: 2, Stands: 1, Events: 1, LastAt: imDay}},
		Capacity: []platformAnalyticsUseCase.CapacityPointView{{At: imDay, AllocatableCPUMillicores: 8000, Agents: 2}},

		FailedLabs: 2, FailedStands: 1, CapacityStepSeconds: 300,
	}}
	w := imServe(t, uc, imStaff(), "/api/analytics/infrastructure?from=2026-09-29T00:00:00Z&to=2026-09-30T00:00:00Z")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if uc.from == nil || !uc.from.Equal(imDay) || uc.to == nil || !uc.to.Equal(imDay.Add(24*time.Hour)) {
		t.Fatalf("period = %v %v", uc.from, uc.to)
	}
	var body struct {
		Data infrastructureResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Data
	if d.Bucket != "hour" || d.Stands.Active != 7 || d.StandHours.Events[0].EventName != "Cup" || d.PeakMax != 3 ||
		d.Failures[0].Code != "image_pull" || d.Capacity[0].Agents != 2 || d.CapacityStepSeconds != 300 || d.FailedLabs != 2 {
		t.Fatalf("body = %+v", d)
	}
	for _, key := range []string{`"StandHours"`, `"EventName"`, `"AllocatableCPUMillicores"`, `"CapacityStepSeconds"`} {
		if !strings.Contains(w.Body.String(), key) {
			t.Fatalf("response lacks the PascalCase key %s", key)
		}
	}
}

func TestInfrastructureRejectsABadPeriod(t *testing.T) {
	uc := &imUseCase{}
	if w := imServe(t, uc, imStaff(), "/api/analytics/infrastructure?from=yesterday"); w.Code != http.StatusBadRequest || uc.calls != 0 {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestInfrastructureCSVTables(t *testing.T) {
	uc := &imUseCase{infra: platformAnalyticsUseCase.InfrastructureView{
		StandHours: platformAnalyticsUseCase.StandHoursView{Events: []platformAnalyticsUseCase.StandHoursEventView{{EventName: "=SUM(A1)", Hours: 1.5, Stands: 2}},
			Kinds: []platformAnalyticsUseCase.StandHoursKindView{{Kind: "event", Hours: 1.5, Labs: 2}, {Kind: "moderators"}, {Kind: "test", Hours: 0.25, Labs: 1}}},
		Peaks:        []platformAnalyticsUseCase.PeakPointView{{At: imDay, Peak: 4}},
		TestLabPeaks: []platformAnalyticsUseCase.PeakPointView{{At: imDay, Peak: 2}},
		AllPeaks:     []platformAnalyticsUseCase.PeakPointView{{At: imDay, Peak: 5}},
		Failures:     []platformAnalyticsUseCase.FailureReasonView{{Code: "crash_loop", Labs: 3, Stands: 1, Events: 2, LastAt: imDay}},
		Capacity:     []platformAnalyticsUseCase.CapacityPointView{{At: imDay, AllocatableCPUMillicores: 8000, RequestedCPUMillicores: 2000, AllocatableMemoryBytes: 100, RequestedMemoryBytes: 50, Agents: 1}},
	}}
	for table, want := range map[string]string{
		"stand_hours": "Захід,Стенд-годин,Стендів\n'=SUM(A1),1.50,2",
		"peaks":       "Час,Пік активних стендів,Пік тестових лабораторій,Пік усіх лабораторій\n2026-09-29T00:00:00Z,4,2,5",
		"kinds":       "Тип,Годин,Лабораторій\nevent,1.50,2\nmoderators,0.00,0\ntest,0.25,1",
		"failures":    "crash_loop,3,1,2,2026-09-29T00:00:00Z",
		"capacity":    "2026-09-29T00:00:00Z,8000,2000,100,50,1",
	} {
		w := imServe(t, uc, imStaff(), "/api/analytics/infrastructure/export.csv?table="+table)
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "\xef\xbb\xbf") || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%s: status = %d body = %q", table, w.Code, w.Body.String())
		}
		if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") || !strings.Contains(w.Header().Get("Content-Disposition"), "platform-infrastructure-"+table) {
			t.Fatalf("%s: headers = %v", table, w.Header())
		}
	}
	uc.calls = 0
	if w := imServe(t, uc, imStaff(), "/api/analytics/infrastructure/export.csv?table=nope"); w.Code != http.StatusBadRequest || uc.calls != 0 {
		t.Fatalf("unknown table: status = %d calls = %d", w.Code, uc.calls)
	}
}

func imMailView() platformAnalyticsUseCase.MailView {
	return platformAnalyticsUseCase.MailView{
		Period: platformAnalyticsModel.Period{From: imDay, To: imDay.Add(24 * time.Hour)},
		Sent:   18, Failed: 2, Total: 20, FailureRate: 0.1, Fallbacks: 1,
		Daily:       []platformAnalyticsUseCase.MailDayView{{Day: imDay, Sent: 18, Failed: 2, Fallbacks: 1}},
		ByTransport: []platformAnalyticsUseCase.MailKeyView{{Key: "platform", Sent: 18, Failed: 2, Total: 20, FailureRate: 0.1}},
		ByChannel:   []platformAnalyticsUseCase.MailKeyView{{Key: "email", Sent: 18, Failed: 2, Deferred: 1, Total: 20, FailureRate: 0.1}},
		ByType:      []platformAnalyticsUseCase.MailKeyView{{Key: "-weird", Sent: 1, Total: 1}},
		Errors:      []platformAnalyticsUseCase.MailErrorView{{Code: "550 5.1.1", Message: "@rejected <address>", Total: 2, LastAt: imDay}},
		Options:     platformAnalyticsUseCase.MailOptionsView{Transports: []string{"platform"}, Types: []string{"user.welcome"}},
	}
}

func TestMailMapsTheReportAndPassesTheFilters(t *testing.T) {
	uc := &imUseCase{mail: imMailView()}
	w := imServe(t, uc, imStaff(), "/api/analytics/mail?from=2026-09-29T00:00:00Z&to=2026-09-30T00:00:00Z&channel=email&transport=platform&type=user.welcome&includeTests=true")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", w.Code, w.Body.String())
	}
	if uc.channel != "email" || uc.transport != "platform" || uc.kind != "user.welcome" || !uc.includeTests || uc.from == nil {
		t.Fatalf("filters = %q %q %q %v", uc.channel, uc.transport, uc.kind, uc.includeTests)
	}
	var body struct {
		Data mailResponse `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	d := body.Data
	if d.Sent != 18 || d.FailureRate != 0.1 || d.Daily[0].Failed != 2 || d.ByTransport[0].Key != "platform" || d.Errors[0].Code != "550 5.1.1" ||
		d.Options.Transports[0] != "platform" || d.Options.Types[0] != "user.welcome" {
		t.Fatalf("body = %+v", d)
	}
}

func TestMailDefaultsExcludeTestsAndRejectABadFlag(t *testing.T) {
	uc := &imUseCase{mail: imMailView()}
	if w := imServe(t, uc, imStaff(), "/api/analytics/mail"); w.Code != http.StatusOK || uc.includeTests || uc.from != nil {
		t.Fatalf("status = %d includeTests = %v from = %v", w.Code, uc.includeTests, uc.from)
	}
	uc.calls = 0
	if w := imServe(t, uc, imStaff(), "/api/analytics/mail?includeTests=maybe"); w.Code != http.StatusBadRequest || uc.calls != 0 {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestMailPassesTheChannelAndRejectsAnUnknownOne(t *testing.T) {
	uc := &imUseCase{mail: imMailView()}
	if w := imServe(t, uc, imStaff(), "/api/analytics/mail?channel=in_app"); w.Code != http.StatusOK || uc.channel != "in_app" {
		t.Fatalf("status = %d channel = %q", w.Code, uc.channel)
	}
	if w := imServe(t, uc, imStaff(), "/api/analytics/mail"); w.Code != http.StatusOK || uc.channel != "" {
		t.Fatalf("no channel = all: status = %d channel = %q", w.Code, uc.channel)
	}
	uc.calls = 0
	for _, url := range []string{"/api/analytics/mail?channel=sms", "/api/analytics/mail/export.csv?table=daily&channel=sms"} {
		if w := imServe(t, uc, imStaff(), url); w.Code != http.StatusBadRequest || uc.calls != 0 {
			t.Fatalf("%s: status = %d", url, w.Code)
		}
	}
}

func TestMailCSVTables(t *testing.T) {
	uc := &imUseCase{mail: imMailView()}
	for table, want := range map[string]string{
		"daily":        "День,Надіслано,З помилкою,Через резервний транспорт\n2026-09-29,18,2,1",
		"by_channel":   "Канал,Надіслано,З помилкою,Відкладено,Усього,Частка помилок\nemail,18,2,1,20,10.00",
		"by_transport": "Транспорт,Надіслано,З помилкою,Усього,Частка помилок,Через резервний транспорт\nplatform,18,2,20,10.00,0",
		"by_type":      "Тип сповіщення,Надіслано,З помилкою,Усього,Частка помилок,Через резервний транспорт\n'-weird,1,0,1,0.00,0",
		"errors":       "Код SMTP,Помилка,Кількість,Востаннє\n550 5.1.1,'@rejected <address>,2,2026-09-29T00:00:00Z",
	} {
		w := imServe(t, uc, imStaff(), "/api/analytics/mail/export.csv?table="+table+"&transport=platform")
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Body.String(), "\xef\xbb\xbf") || !strings.Contains(w.Body.String(), want) {
			t.Fatalf("%s: status = %d body = %q", table, w.Code, w.Body.String())
		}
		if uc.transport != "platform" {
			t.Fatalf("%s: the export must carry the filters", table)
		}
	}
	uc.calls = 0
	if w := imServe(t, uc, imStaff(), "/api/analytics/mail/export.csv?table=nope"); w.Code != http.StatusBadRequest || uc.calls != 0 {
		t.Fatalf("unknown table: status = %d", w.Code)
	}
}
