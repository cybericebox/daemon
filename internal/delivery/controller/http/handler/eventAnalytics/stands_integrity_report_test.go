package eventAnalytics

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// sectionsUseCase serves the stands, integrity and report routes; the other
// calls panic on the nil embedded interface.
type sectionsUseCase struct {
	IUseCase
	levels     []eventAnalyticsUseCase.Level
	thresholds eventAnalyticsModel.IntegrityThresholds
	filter     eventAnalyticsUseCase.IntegrityFilter
	integrity  eventAnalyticsUseCase.IntegrityView
	flags      []eventAnalyticsUseCase.IntegrityFlag
	reviewed   []string
	unreviewed []uuid.UUID
	dismissed  []string
	removed    []uuid.UUID
	report     eventAnalyticsUseCase.ReportView
	stands     eventAnalyticsUseCase.StandsView
}

func (f *sectionsUseCase) RequireEventAnalytics(_ context.Context, _ uuid.UUID, _ rbac.Claims, level eventAnalyticsUseCase.Level) error {
	f.levels = append(f.levels, level)
	return nil
}

func (f *sectionsUseCase) GetEventAnalyticsIntegrity(_ context.Context, _ uuid.UUID, _, _ *time.Time, th eventAnalyticsModel.IntegrityThresholds, filter eventAnalyticsUseCase.IntegrityFilter) (eventAnalyticsUseCase.IntegrityView, error) {
	f.thresholds, f.filter = th, filter
	return f.integrity, nil
}

func (f *sectionsUseCase) GetEventAnalyticsIntegrityFlags(context.Context, uuid.UUID) ([]eventAnalyticsUseCase.IntegrityFlag, error) {
	return f.flags, nil
}

func (f *sectionsUseCase) ReviewEventSolve(_ context.Context, _, tc, reviewer uuid.UUID, note string) error {
	f.reviewed = append(f.reviewed, tc.String()+"|"+reviewer.String()+"|"+note)
	return nil
}

func (f *sectionsUseCase) DismissIntegrityPattern(_ context.Context, _, tc, _ uuid.UUID, scope eventAnalyticsModel.DismissScope, kind eventAnalyticsModel.IntegrityKind, key, note string) error {
	f.dismissed = append(f.dismissed, strings.Join([]string{tc.String(), string(scope), string(kind), key, note}, "|"))
	return nil
}

func (f *sectionsUseCase) ListIntegrityDismissals(context.Context, uuid.UUID) ([]eventAnalyticsUseCase.DismissalView, error) {
	return []eventAnalyticsUseCase.DismissalView{{ID: uuid.UUID{15: 5}, Scope: eventAnalyticsModel.DismissExercise, Kind: eventAnalyticsModel.IntegritySharedWrong, Key: "ice{x}"}}, nil
}

func (f *sectionsUseCase) RemoveIntegrityDismissal(_ context.Context, _, id uuid.UUID) error {
	f.removed = append(f.removed, id)
	return nil
}

func (f *sectionsUseCase) UnreviewEventSolve(_ context.Context, _, tc uuid.UUID) error {
	f.unreviewed = append(f.unreviewed, tc)
	return nil
}

func (f *sectionsUseCase) GetEventAnalyticsReport(context.Context, uuid.UUID) (eventAnalyticsUseCase.ReportView, error) {
	return f.report, nil
}

func (f *sectionsUseCase) GetEventAnalyticsStands(context.Context, uuid.UUID, *time.Time, *time.Time) (eventAnalyticsUseCase.StandsView, error) {
	return f.stands, nil
}

func serveSections(t *testing.T, uc IUseCase, target string) *httptest.ResponseRecorder {
	t.Helper()
	return serveSectionsWith(t, uc, http.MethodGet, target, "")
}

func serveSectionsWith(t *testing.T, uc IUseCase, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), rbac.Claims{UserID: uuid.Must(uuid.NewV7()), Role: rbac.RoleUser}))
	}, response.WithErrorHandler)
	NewEventAnalyticsAPIHandler(uc, allowAll{}).Init(router.Group("api"))
	w := httptest.NewRecorder()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	return w
}

func sectionsURL(route string) string {
	return "/api/events/" + uuid.Must(uuid.NewV7()).String() + "/manage/analytics/" + route
}

func TestIntegrityIsSensitiveAndReadsThresholdsAndFilter(t *testing.T) {
	uc := &sectionsUseCase{}
	team, task := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	w := serveSections(t, uc, sectionsURL("integrity?floorElementary=3&floorHard=200&bruteForceAttempts=9&bruteForceWindow=45&followGap=20&signal=too_fast&reviewed=no&teamId="+team.String()+"&challengeId="+task.String()))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, []eventAnalyticsUseCase.Level{eventAnalyticsUseCase.LevelSensitive}, uc.levels)
	want := eventAnalyticsModel.DefaultIntegrityThresholds()
	want.Floors["elementary"], want.Floors["hard"] = 3*time.Second, 200*time.Second
	want.BruteForceAttempts, want.BruteForceWindow, want.FollowGap = 9, 45*time.Second, 20*time.Second
	require.Equal(t, want, uc.thresholds)
	require.Equal(t, eventAnalyticsUseCase.IntegrityFilter{
		Signal: eventAnalyticsModel.IntegrityTooFast, TeamID: team, ChallengeID: task, Reviewed: eventAnalyticsUseCase.ReviewedNotYet,
	}, uc.filter)

	// No parameters: the defaults and no filter.
	uc = &sectionsUseCase{}
	require.Equal(t, http.StatusOK, serveSections(t, uc, sectionsURL("integrity")).Code)
	require.Equal(t, eventAnalyticsModel.DefaultIntegrityThresholds(), uc.thresholds)
	require.Equal(t, eventAnalyticsUseCase.IntegrityFilter{}, uc.filter)

	for _, bad := range []string{"floorHard=slow", "signal=same_answer", "reviewed=maybe", "teamId=nope", "challengeId=1"} {
		require.Equal(t, http.StatusBadRequest, serveSections(t, &sectionsUseCase{}, sectionsURL("integrity?"+bad)).Code, bad)
	}
}

func TestIntegrityResponseCarriesEvidence(t *testing.T) {
	item := eventAnalyticsUseCase.IntegrityItem{FlaggedSolve: eventAnalyticsModel.FlaggedSolve{
		Team: eventAnalyticsModel.IntegrityTeam{Name: "A"}, ChallengeName: "Web", Level: "easy",
		Signals: []eventAnalyticsModel.IntegritySignal{{Kind: eventAnalyticsModel.IntegrityTooFast, Seconds: 3, Baseline: 20}},
	}}
	uc := &sectionsUseCase{integrity: eventAnalyticsUseCase.IntegrityView{Items: []eventAnalyticsUseCase.IntegrityItem{item}, Total: 1,
		Counts: map[eventAnalyticsModel.IntegrityKind]int{eventAnalyticsModel.IntegrityTooFast: 1}}}
	w := serveSections(t, uc, sectionsURL("integrity"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"Kind":"too_fast"`)
	require.Contains(t, w.Body.String(), `"Seconds":3`)
	require.Contains(t, w.Body.String(), `"Baseline":20`)
	require.Contains(t, w.Body.String(), `"no_access":0`, "every kind has a count")
	require.Contains(t, w.Body.String(), `"Review":null`)
}

func TestIntegrityExportIsSensitiveToo(t *testing.T) {
	uc := &sectionsUseCase{integrity: eventAnalyticsUseCase.IntegrityView{Items: []eventAnalyticsUseCase.IntegrityItem{{
		FlaggedSolve: eventAnalyticsModel.FlaggedSolve{
			Team: eventAnalyticsModel.IntegrityTeam{Name: "=cmd()"}, ChallengeName: "Web", Level: "easy",
			Signals: []eventAnalyticsModel.IntegritySignal{{Kind: eventAnalyticsModel.IntegrityNoAccess}, {Kind: eventAnalyticsModel.IntegrityTooFast}},
		},
	}}}}
	w := serveSections(t, uc, sectionsURL("integrity/export.csv"))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []eventAnalyticsUseCase.Level{eventAnalyticsUseCase.LevelSensitive}, uc.levels)
	require.Contains(t, w.Body.String(), "'=cmd()", "formulas in cells are neutralized")
	require.Contains(t, w.Body.String(), "no_access; too_fast")
}

func TestIntegrityFlagsAndReviewsAreSensitive(t *testing.T) {
	tc := uuid.Must(uuid.NewV7())
	uc := &sectionsUseCase{flags: []eventAnalyticsUseCase.IntegrityFlag{{TeamChallengeID: tc, Signals: []eventAnalyticsModel.IntegrityKind{eventAnalyticsModel.IntegrityNoAccess, eventAnalyticsModel.IntegrityBurst}}}}
	w := serveSections(t, uc, sectionsURL("integrity/flags"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"Count":2`)
	require.NotContains(t, w.Body.String(), "Name", "the journal marker carries no names")

	url := sectionsURL("integrity/solves/" + tc.String() + "/review")
	w = serveSectionsWith(t, uc, http.MethodPut, url, `{"Note":"checked"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Len(t, uc.reviewed, 1)
	require.True(t, strings.HasPrefix(uc.reviewed[0], tc.String()+"|"))
	require.True(t, strings.HasSuffix(uc.reviewed[0], "|checked"))

	require.Equal(t, http.StatusOK, serveSectionsWith(t, uc, http.MethodDelete, url, "").Code)
	require.Equal(t, []uuid.UUID{tc}, uc.unreviewed)
	require.Equal(t, http.StatusBadRequest, serveSectionsWith(t, uc, http.MethodPut, sectionsURL("integrity/solves/nope/review"), `{}`).Code)
	require.Equal(t, http.StatusBadRequest, serveSectionsWith(t, uc, http.MethodPut, url, `{`).Code)
	for _, level := range uc.levels {
		require.Equal(t, eventAnalyticsUseCase.LevelSensitive, level)
	}
}

func TestStandsNeedSectionAccess(t *testing.T) {
	uc := &sectionsUseCase{stands: eventAnalyticsUseCase.StandsView{Available: true, Teams: []eventAnalyticsUseCase.StandTeamView{{TeamName: "A", Status: "ready"}}}}
	w := serveSections(t, uc, sectionsURL("stands"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, []eventAnalyticsUseCase.Level{eventAnalyticsUseCase.LevelSections}, uc.levels)
	require.Contains(t, w.Body.String(), `"TeamName":"A"`)

	w = serveSections(t, uc, sectionsURL("stands/export.csv"))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), "ready")
}

func TestReportZipHoldsTheTablesAfterTheFinish(t *testing.T) {
	finish := time.Date(2026, 9, 29, 16, 0, 0, 0, time.UTC)
	uc := &sectionsUseCase{report: eventAnalyticsUseCase.ReportView{
		Available: true, EventName: "=Evil", FinishAt: &finish,
		Ranking:           []eventAnalyticsUseCase.ReportRankView{{Rank: 1, Name: "A", Points: 100}},
		TaskRows:          []eventAnalyticsUseCase.ReportTaskView{{Name: "Web", Solves: 1}},
		ParticipantFunnel: []eventAnalyticsUseCase.FunnelStepView{{Key: "registered", Count: 5}},
		Series:            []eventAnalyticsUseCase.SeriesPointView{{At: finish, Attempts: 2}, {At: finish.Add(5 * time.Minute)}},
	}}
	w := serveSections(t, uc, sectionsURL("report/export.zip"))
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "application/zip", w.Header().Get("Content-Type"))

	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	require.NoError(t, err)
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	require.Equal(t, []string{"activity.csv", "funnel.csv", "ranking.csv", "summary.csv", "tasks.csv"}, names)
	summary, err := zr.Open("summary.csv")
	require.NoError(t, err)
	defer summary.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(summary)
	require.Contains(t, buf.String(), "'=Evil")
}

func TestReportZipBeforeTheFinishIsConflict(t *testing.T) {
	w := serveSections(t, &sectionsUseCase{}, sectionsURL("report/export.zip"))
	require.Equal(t, http.StatusConflict, w.Code, w.Body.String())

	w = serveSections(t, &sectionsUseCase{}, sectionsURL("report"))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"Available":false`)
}

func TestIntegrityDismissalsAreSensitive(t *testing.T) {
	tc := uuid.Must(uuid.NewV7())
	uc := &sectionsUseCase{}
	w := serveSections(t, uc, sectionsURL("integrity/dismissals"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"Scope":"exercise"`)

	url := sectionsURL("integrity/dismissals")
	body := `{"TeamChallengeID":"` + tc.String() + `","Kind":"shared_wrong","Key":"ICE{x}","Scope":"event","Note":"decoy"}`
	require.Equal(t, http.StatusOK, serveSectionsWith(t, uc, http.MethodPost, url, body).Code)
	require.Equal(t, []string{tc.String() + "|event|shared_wrong|ICE{x}|decoy"}, uc.dismissed)
	require.Equal(t, http.StatusBadRequest, serveSectionsWith(t, uc, http.MethodPost, url, `{`).Code)

	id := uuid.Must(uuid.NewV7())
	require.Equal(t, http.StatusOK, serveSectionsWith(t, uc, http.MethodDelete, sectionsURL("integrity/dismissals/"+id.String()), "").Code)
	require.Equal(t, []uuid.UUID{id}, uc.removed)
	require.Equal(t, http.StatusBadRequest, serveSectionsWith(t, uc, http.MethodDelete, sectionsURL("integrity/dismissals/nope"), "").Code)
	for _, level := range uc.levels {
		require.Equal(t, eventAnalyticsUseCase.LevelSensitive, level)
	}
}

func TestIntegrityResponseCarriesCrossFlagAndAnswers(t *testing.T) {
	at := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	item := eventAnalyticsUseCase.IntegrityItem{FlaggedSolve: eventAnalyticsModel.FlaggedSolve{
		Team: eventAnalyticsModel.IntegrityTeam{Name: "Thief"}, ChallengeName: "Web", Level: "hard", At: at,
		Signals: []eventAnalyticsModel.IntegritySignal{
			{Kind: eventAnalyticsModel.IntegrityCrossFlag, Count: 2, At: at, Owner: &eventAnalyticsModel.IntegrityCrossOwner{
				Team: eventAnalyticsModel.IntegrityTeam{Name: "Victim"}, ChallengeName: "Crypto"}},
			{Kind: eventAnalyticsModel.IntegritySharedWrong, Info: true, Count: 1, Answers: []eventAnalyticsModel.SharedAnswer{{
				Value: "ice{x}", Order: []eventAnalyticsModel.IntegritySubmission{{Team: eventAnalyticsModel.IntegrityTeam{Name: "B"}, At: at}}}}},
		},
	}}
	uc := &sectionsUseCase{integrity: eventAnalyticsUseCase.IntegrityView{Items: []eventAnalyticsUseCase.IntegrityItem{item}, Total: 1}}
	w := serveSections(t, uc, sectionsURL("integrity"))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"Solved":false`)
	require.Contains(t, w.Body.String(), `"Owner":{"TeamID":"00000000-0000-0000-0000-000000000000","TeamName":"Victim"`)
	require.Contains(t, w.Body.String(), `"Value":"ice{x}"`)
	require.Contains(t, w.Body.String(), `"Info":true`)
}
