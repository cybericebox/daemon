package eventself

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventResultRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestOwnChallengeResponseIncludesBoardMetadataWithoutFlag(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	groupID := uuid.Must(uuid.NewV7())
	body, err := json.Marshal(toOwnChallengeResponse(eventUseCase.OwnChallengeView{
		ID: id, EventChallengeID: uuid.Must(uuid.NewV7()),
		Snapshot: json.RawMessage(`{"name":"Assigned task"}`),
		Points:   250, GroupID: &groupID, GroupName: "Web", GroupOrder: 2,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"Points":250`, `"GroupName":"Web"`, `"name":"Assigned task"`} {
		if !strings.Contains(string(body), expected) {
			t.Fatalf("missing %s in %s", expected, body)
		}
	}
	if strings.Contains(string(body), "ExpectedFlag") {
		t.Fatalf("participant response leaked the expected flag: %s", body)
	}
}

type flushRecorder struct{ *httptest.ResponseRecorder }

func (flushRecorder) Flush() {}

func TestWriteLiveReplayRequiresSnapshotForPopularityRecalculation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := flushRecorder{httptest.NewRecorder()}
	ctx, _ := gin.CreateTestContext(recorder)
	last := int64(7)
	if writeLiveReplay(ctx, recorder, eventUseCase.LiveResultsReplay{Changes: []eventUseCase.LiveResultChangeView{{Revision: 8, Kind: string(eventResultRepo.ChangeScoreboardRecalculated)}}}, &last) {
		t.Fatal("popularity recalculation must require a fresh snapshot")
	}
	if got := recorder.Body.String(); !strings.Contains(got, "event: snapshot-required") || last != 7 {
		t.Fatalf("live response=%q revision=%d", got, last)
	}
}

type fakeUseCase struct {
	info         func(context.Context, uuid.UUID) (eventUseCase.EventInfoView, error)
	approvedInfo func(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.ParticipantEventInfoView, error)
	vpnConfig    func(context.Context, uuid.UUID, uuid.UUID) (string, error)
	vpnStatus    func(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.VPNProbeStatusView, error)
	readErr      error
	createTeam   func(context.Context, uuid.UUID, uuid.UUID, string) error
	joinTeam     func(context.Context, uuid.UUID, uuid.UUID, string) error
	results      func(context.Context, uuid.UUID, eventUseCase.ResultsAccess) (eventUseCase.ResultsSnapshotView, error)
	live         func(context.Context, uuid.UUID, eventUseCase.ResultsAccess, int64) (eventUseCase.LiveResultsReplay, error)
	content      func(context.Context, uuid.UUID, eventUseCase.ContentAccess) (eventUseCase.PublicEventContentView, error)
	page         func(context.Context, uuid.UUID, string, eventUseCase.ContentAccess) (eventUseCase.PublicEventPageView, error)
	pageAccess   func(context.Context, uuid.UUID, string) error
	getOwnForm   func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (eventUseCase.EventFormView, error)
	attachment   func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (io.ReadCloser, mediaModel.File, error)
	solves       func(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) ([]eventUseCase.ChallengeSolveView, error)
	members      func(context.Context, uuid.UUID, uuid.UUID) ([]eventUseCase.TeamRosterMemberView, error)
	answers      func(context.Context, uuid.UUID, uuid.UUID, map[string]any) (eventUseCase.OwnParticipantAnswersView, error)
	answerFile   func() (io.ReadCloser, eventUseCase.AnswerFileView, error)
}

func (f fakeUseCase) UploadAnswerFile(context.Context, uuid.UUID, uuid.UUID, eventFormModel.AnswerScope, string, string, io.Reader) (eventUseCase.AnswerFileView, error) {
	return eventUseCase.AnswerFileView{}, f.readErr
}

func (f fakeUseCase) StreamOwnAnswerFile(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (io.ReadCloser, eventUseCase.AnswerFileView, error) {
	if f.answerFile != nil {
		return f.answerFile()
	}
	return nil, eventUseCase.AnswerFileView{}, f.readErr
}

func (f fakeUseCase) ListChallengeSolves(ctx context.Context, eventID, userID, challengeID, _ uuid.UUID, _ int) (eventUseCase.ChallengeSolvesPage, error) {
	if f.solves != nil {
		items, err := f.solves(ctx, eventID, userID, challengeID)
		return eventUseCase.ChallengeSolvesPage{Items: items, Total: int64(len(items))}, err
	}
	return eventUseCase.ChallengeSolvesPage{}, nil
}
func (f fakeUseCase) ListOwnTeamMembers(ctx context.Context, eventID, userID uuid.UUID) ([]eventUseCase.TeamRosterMemberView, error) {
	if f.members != nil {
		return f.members(ctx, eventID, userID)
	}
	return nil, nil
}
func (f fakeUseCase) GetOwnParticipantAnswers(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.OwnParticipantAnswersView, error) {
	if f.answers != nil {
		return f.answers(ctx, eventID, userID, nil)
	}
	return eventUseCase.OwnParticipantAnswersView{}, nil
}
func (f fakeUseCase) UpdateOwnParticipantAnswers(ctx context.Context, eventID, userID uuid.UUID, answers map[string]any) (eventUseCase.OwnParticipantAnswersView, error) {
	if f.answers != nil {
		return f.answers(ctx, eventID, userID, answers)
	}
	return eventUseCase.OwnParticipantAnswersView{}, nil
}

func (f fakeUseCase) StreamOwnChallengeAttachment(ctx context.Context, eventID, userID, challengeID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	if f.attachment != nil {
		return f.attachment(ctx, eventID, userID, challengeID, fileID)
	}
	return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
}

func (f fakeUseCase) GetApprovedParticipantInfo(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.ParticipantEventInfoView, error) {
	if f.approvedInfo != nil {
		return f.approvedInfo(ctx, eventID, userID)
	}
	return eventUseCase.ParticipantEventInfoView{}, nil
}

func TestEventInfoSeparatesApplicantFromApprovedParticipant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	view := eventUseCase.EventInfoView{
		EventID: eventID, Status: eventModel.LifecyclePublished,
		Infrastructure:         eventUseCase.EventInfrastructurePlan{RequiresVPN: true, HasDynamicLabs: true},
		ScoreboardVisibility:   eventConfigModel.VisibilityPrivate,
		ParticipantsVisibility: eventConfigModel.VisibilityHidden,
	}
	useCase := fakeUseCase{
		info: func(context.Context, uuid.UUID) (eventUseCase.EventInfoView, error) { return view, nil },
		approvedInfo: func(_ context.Context, gotEvent, gotUser uuid.UUID) (eventUseCase.ParticipantEventInfoView, error) {
			if gotEvent != eventID || gotUser != userID {
				t.Fatalf("projection used wrong identity: %s %s", gotEvent, gotUser)
			}
			return eventUseCase.ParticipantEventInfoView{EventID: eventID, UseVPN: true, CanViewResults: true, CanViewParticipants: false}, nil
		},
	}
	h := NewEventSelfAPIHandler(useCase, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/events/self/info", nil)
	request = request.WithContext(middleware.ContextWithEventTenant(request.Context(), middleware.EventTenant{EventID: eventID}))
	request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = request
	h.info(ctx)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "Infrastructure") || strings.Contains(w.Body.String(), "UseVPN") {
		t.Fatalf("applicant info leaked capability or lab plan: %d %s", w.Code, w.Body.String())
	}
	for _, forbidden := range []string{"ScoreboardVisibility", "ParticipantsVisibility", "PublishAt", "WithdrawAt", "ManualFinishAt"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("applicant info leaked %s: %s", forbidden, w.Body.String())
		}
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("applicant info cache policy: %q", got)
	}
	w = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(w)
	ctx.Request = request
	h.participantInfo(ctx)
	// The lab plan object stays out; only the derived HasInfrastructureChallenges flag is exposed.
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"UseVPN":true`) || strings.Contains(w.Body.String(), `"Infrastructure":`) {
		t.Fatalf("approved projection mismatch: %d %s", w.Code, w.Body.String())
	}
	for _, expected := range []string{`"ShowDifficulty":false`, `"HintsDisabled":false`, `"HasInfrastructureChallenges":false`} {
		if !strings.Contains(w.Body.String(), expected) {
			t.Fatalf("participant info misses %s: %s", expected, w.Body.String())
		}
	}
	if w.Code != http.StatusOK {
		t.Fatalf("approved projection mismatch: %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"CanViewResults":true`) || !strings.Contains(w.Body.String(), `"CanViewParticipants":false`) {
		t.Fatalf("approved navigation capabilities are wrong: %s", w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("participant info cache policy: %q", got)
	}
	h.useCase = fakeUseCase{approvedInfo: func(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.ParticipantEventInfoView, error) {
		return eventUseCase.ParticipantEventInfoView{}, participantModel.ErrParticipantAccessForbidden.Err()
	}}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	router.GET("/api/events/self/participant-info", func(ctx *gin.Context) {
		ctx.Request = ctx.Request.WithContext(request.Context())
		h.participantInfo(ctx)
	})
	w = httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/events/self/participant-info", nil))
	if w.Code != http.StatusForbidden {
		t.Fatalf("pending participant received projection: %d %s", w.Code, w.Body.String())
	}
}

func (f fakeUseCase) GetEventInfo(ctx context.Context, eventID uuid.UUID) (eventUseCase.EventInfoView, error) {
	if f.info != nil {
		return f.info(ctx, eventID)
	}
	return eventUseCase.EventInfoView{}, nil
}

func (f fakeUseCase) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return f.readErr
}

func TestPublicInfo_OnlyExposesPublishedIdentityAndTheme(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	view := eventUseCase.EventInfoView{
		EventID: eventID, Name: "Подія", Status: eventModel.LifecyclePublished,
		Theme:                  eventConfigModel.DefaultTheme(),
		Infrastructure:         eventUseCase.EventInfrastructurePlan{HasDynamicLabs: true, RequiresVPN: true},
		ScoreboardVisibility:   eventConfigModel.VisibilityPrivate,
		ParticipantsVisibility: eventConfigModel.VisibilityPublic,
	}
	h := NewEventSelfAPIHandler(fakeUseCase{info: func(_ context.Context, got uuid.UUID) (eventUseCase.EventInfoView, error) {
		if got != eventID {
			t.Fatalf("wrong tenant event: %s", got)
		}
		return view, nil
	}}, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/events/self/public-info", nil)
	request = request.WithContext(middleware.ContextWithEventTenant(request.Context(), middleware.EventTenant{EventID: eventID}))
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = request
	h.publicInfo(ctx)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"EventID":"`+eventID.String()+`"`) || !strings.Contains(w.Body.String(), `"Brand":"#211A52"`) || strings.Contains(w.Body.String(), "Infrastructure") || strings.Contains(w.Body.String(), "UseVPN") {
		t.Fatalf("unexpected public response: %d %s", w.Code, w.Body.String())
	}
	for _, forbidden := range []string{"ScoreboardVisibility", "ParticipantsVisibility", "PublishAt", "WithdrawAt", "ManualFinishAt"} {
		if strings.Contains(w.Body.String(), forbidden) {
			t.Fatalf("public info leaked %s: %s", forbidden, w.Body.String())
		}
	}
	if strings.Contains(w.Body.String(), `"CanViewResults":true`) || !strings.Contains(w.Body.String(), `"CanViewParticipants":true`) {
		t.Fatalf("public navigation capabilities are wrong: %s", w.Body.String())
	}

	view.Status = eventModel.LifecycleNotPublished
	w = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(w)
	ctx.Request = request
	h.publicInfo(ctx)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unpublished event leaked: %d %s", w.Code, w.Body.String())
	}

	managerID := uuid.Must(uuid.NewV7())
	managerRequest := request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: managerID}))
	w = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(w)
	ctx.Request = managerRequest
	h.publicInfo(ctx)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"EventID":"`+eventID.String()+`"`) {
		t.Fatalf("manager could not read unpublished event identity: %d %s", w.Code, w.Body.String())
	}

	h.useCase = fakeUseCase{info: func(context.Context, uuid.UUID) (eventUseCase.EventInfoView, error) {
		return view, nil
	}, readErr: eventManagerModel.ErrEventManagementForbidden.Err()}
	w = httptest.NewRecorder()
	ctx, _ = gin.CreateTestContext(w)
	ctx.Request = managerRequest
	h.publicInfo(ctx)
	if w.Code != http.StatusNotFound {
		t.Fatalf("unpublished event leaked to non-manager: %d %s", w.Code, w.Body.String())
	}
}
func (f fakeUseCase) GetPublicEventContent(ctx context.Context, eventID uuid.UUID, access eventUseCase.ContentAccess) (eventUseCase.PublicEventContentView, error) {
	if f.content != nil {
		return f.content(ctx, eventID, access)
	}
	return eventUseCase.PublicEventContentView{}, nil
}
func (fakeUseCase) ListNavigationPages(context.Context, uuid.UUID, eventUseCase.ContentAccess) ([]eventUseCase.NavigationPageView, error) {
	return nil, nil
}
func (f fakeUseCase) GetPublicEventPage(ctx context.Context, eventID uuid.UUID, slug string, access eventUseCase.ContentAccess) (eventUseCase.PublicEventPageView, error) {
	if f.page != nil {
		return f.page(ctx, eventID, slug, access)
	}
	return eventUseCase.PublicEventPageView{}, nil
}

func (f fakeUseCase) RequirePublicEventPage(ctx context.Context, eventID uuid.UUID, slug string) error {
	if f.pageAccess != nil {
		return f.pageAccess(ctx, eventID, slug)
	}
	return nil
}

func (f fakeUseCase) GetJoinInfo(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.JoinInfoView, error) {
	return eventUseCase.JoinInfoView{}, nil
}
func (fakeUseCase) GetParticipantForm(context.Context, uuid.UUID) (eventUseCase.ParticipantFormView, error) {
	return eventUseCase.ParticipantFormView{}, nil
}
func (fakeUseCase) SubmitParticipantForm(context.Context, uuid.UUID, uuid.UUID, eventUseCase.SubmitParticipantFormInput) (eventUseCase.ParticipantFormAnswerView, error) {
	return eventUseCase.ParticipantFormAnswerView{}, nil
}
func (fakeUseCase) ListPendingEventForms(context.Context, uuid.UUID, uuid.UUID) ([]eventUseCase.PendingEventFormView, error) {
	return nil, nil
}
func (fakeUseCase) GetEventForm(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.EventFormView, error) {
	return eventUseCase.EventFormView{}, nil
}
func (f fakeUseCase) GetOwnEventForm(ctx context.Context, eventID, formID, userID uuid.UUID) (eventUseCase.EventFormView, error) {
	if f.getOwnForm == nil {
		return eventUseCase.EventFormView{}, nil
	}
	return f.getOwnForm(ctx, eventID, formID, userID)
}
func (fakeUseCase) SubmitEventFormResponse(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, eventUseCase.SubmitEventFormResponseInput) error {
	return nil
}

func (f fakeUseCase) JoinEvent(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.JoinInfoView, error) {
	return eventUseCase.JoinInfoView{}, nil
}

func (f fakeUseCase) CreateTeam(ctx context.Context, eventID, userID uuid.UUID, name string) error {
	return f.createTeam(ctx, eventID, userID, name)
}
func (f fakeUseCase) CreateTeamWithFields(ctx context.Context, eventID, userID uuid.UUID, name string, _ map[string]any) error {
	return f.createTeam(ctx, eventID, userID, name)
}
func (fakeUseCase) AcceptParticipantInvitation(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.JoinInfoView, error) {
	return eventUseCase.JoinInfoView{}, nil
}
func (fakeUseCase) DeclineParticipantInvitation(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}
func (fakeUseCase) SetOwnPseudonym(context.Context, uuid.UUID, uuid.UUID, *string) (eventUseCase.ParticipantNameView, error) {
	return eventUseCase.ParticipantNameView{}, nil
}
func (fakeUseCase) UpdateOwnTeamFields(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, map[string]any) (eventUseCase.OwnTeamView, error) {
	return eventUseCase.OwnTeamView{}, nil
}
func (fakeUseCase) GetTeamFields(context.Context, uuid.UUID) (eventUseCase.ParticipantFormView, error) {
	return eventUseCase.ParticipantFormView{}, nil
}

func (f fakeUseCase) JoinTeam(ctx context.Context, eventID, userID uuid.UUID, joinCode string) error {
	return f.joinTeam(ctx, eventID, userID, joinCode)
}

func (fakeUseCase) GetOwnTeam(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.OwnTeamView, error) {
	return eventUseCase.OwnTeamView{}, nil
}
func (fakeUseCase) LeaveTeam(context.Context, uuid.UUID, uuid.UUID) error { return nil }
func (fakeUseCase) RenameTeam(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (fakeUseCase) RegenerateTeamJoinCode(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, eventTeamModel.JoinCodeExpiry) error {
	return nil
}
func (fakeUseCase) TransferTeamCaptaincy(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (fakeUseCase) KickTeamMember(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error { return nil }
func (fakeUseCase) DisbandTeam(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error    { return nil }
func (fakeUseCase) UnlockHint(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (eventUseCase.OwnHintView, error) {
	return eventUseCase.OwnHintView{}, nil
}
func (fakeUseCase) SubmitChallenge(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, eventUseCase.SubmitChallengeInput) (eventUseCase.SubmitChallengeResult, error) {
	return eventUseCase.SubmitChallengeResult{}, nil
}
func (fakeUseCase) ListOwnBoard(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.OwnBoardView, error) {
	return eventUseCase.OwnBoardView{}, nil
}
func (f fakeUseCase) GetResultsSnapshot(ctx context.Context, eventID uuid.UUID, access eventUseCase.ResultsAccess, liveScreen bool) (eventUseCase.ResultsSnapshotView, error) {
	if f.results != nil {
		return f.results(ctx, eventID, access)
	}
	return eventUseCase.ResultsSnapshotView{}, nil
}
func (f fakeUseCase) OpenLiveResults(eventID uuid.UUID, access eventUseCase.ResultsAccess, liveScreen bool) eventUseCase.LiveResultsSubscription {
	return fakeLiveSubscription{f: f, eventID: eventID, access: access}
}

type fakeLiveSubscription struct {
	f       fakeUseCase
	eventID uuid.UUID
	access  eventUseCase.ResultsAccess
}

func (s fakeLiveSubscription) Replay(ctx context.Context, afterRevision int64) (eventUseCase.LiveResultsReplay, error) {
	if s.f.live != nil {
		return s.f.live(ctx, s.eventID, s.access, afterRevision)
	}
	return eventUseCase.LiveResultsReplay{}, nil
}
func (fakeUseCase) GetOwnTeamResults(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.OwnTeamResultsView, error) {
	return eventUseCase.OwnTeamResultsView{}, nil
}

func (fakeUseCase) GetModeratorsParticipationStats(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.ParticipationStatsView, error) {
	return eventUseCase.ParticipationStatsView{}, nil
}

func (fakeUseCase) GetParticipationStats(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.ParticipationStatsView, error) {
	return eventUseCase.ParticipationStatsView{}, nil
}

func TestGetForm_UsesDeliveredFormForSessionUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	formID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	called := false
	h := NewEventSelfAPIHandler(fakeUseCase{getOwnForm: func(_ context.Context, gotEventID, gotFormID, gotUserID uuid.UUID) (eventUseCase.EventFormView, error) {
		called = true
		if gotEventID != eventID || gotFormID != formID || gotUserID != userID {
			t.Fatalf("unexpected own-form arguments: event=%s form=%s user=%s", gotEventID, gotFormID, gotUserID)
		}
		return eventUseCase.EventFormView{ID: formID, CurrentVersionID: uuid.Must(uuid.NewV7()), Version: 2}, nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/forms/"+formID.String(), nil)
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}, {Key: "formID", Value: formID.String()}}

	h.getForm(ctx)

	if !called {
		t.Fatal("GetOwnEventForm use case was not called")
	}
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Version":2`) {
		t.Fatalf("unexpected response: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestResultsSnapshot_AllowsAnonymousRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{results: func(_ context.Context, gotEventID uuid.UUID, access eventUseCase.ResultsAccess) (eventUseCase.ResultsSnapshotView, error) {
		if gotEventID != eventID || access.UserID != nil || access.Role != rbac.RolePublic {
			t.Fatalf("unexpected access: %#v", access)
		}
		return eventUseCase.ResultsSnapshotView{Revision: 3}, nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/results", nil)
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	h.resultsSnapshot(ctx)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Revision":3`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}

func TestPublicContent_UsesOptionalSessionAccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{content: func(_ context.Context, gotEventID uuid.UUID, access eventUseCase.ContentAccess) (eventUseCase.PublicEventContentView, error) {
		if gotEventID != eventID || access.UserID != nil || access.Role != rbac.RolePublic {
			t.Fatalf("unexpected content access: event=%s access=%#v", gotEventID, access)
		}
		return eventUseCase.PublicEventContentView{Variables: map[string]any{"event.name": "Games"}}, nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/content", nil)
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	h.publicContent(ctx)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"event.name"`) {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body.String())
	}
}

func TestPublicContentDocumentAndValuesAreSeparate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{content: func(_ context.Context, gotEventID uuid.UUID, access eventUseCase.ContentAccess) (eventUseCase.PublicEventContentView, error) {
		if gotEventID != eventID || access.UserID != nil || access.Role != rbac.RolePublic {
			t.Fatalf("unexpected public content access: event=%s access=%#v", gotEventID, access)
		}
		return eventUseCase.PublicEventContentView{
			Landing:   eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "intro", Type: eventContentModel.BlockSection, Label: "Welcome"}}},
			Variables: map[string]any{"event.name": "Games"},
		}, nil
	}}, nil)
	for _, tc := range []struct {
		name, path, want, absent string
		handler                  gin.HandlerFunc
	}{
		{name: "document", path: "/document", want: `"Landing"`, absent: `"Variables"`, handler: h.publicContentDocument},
		{name: "values", path: "/values", want: `"event.name"`, absent: `"Landing"`, handler: h.publicContentValues},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/content"+tc.path, nil)
			ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
			tc.handler(ctx)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), tc.absent) {
				t.Fatalf("unexpected %s response: %d %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestPublicPageDocumentAndValuesUsePublicAudience(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{page: func(_ context.Context, gotEventID uuid.UUID, slug string, access eventUseCase.ContentAccess) (eventUseCase.PublicEventPageView, error) {
		if gotEventID != eventID || slug != "rules" || access.UserID != nil || access.Role != rbac.RolePublic {
			t.Fatalf("unexpected page access: event=%s slug=%q access=%#v", gotEventID, slug, access)
		}
		return eventUseCase.PublicEventPageView{
			Page:      eventUseCase.EventPageView{Slug: "rules", Title: "Rules", Visibility: eventContentModel.PageVisibilityPublic},
			Variables: map[string]any{"event.name": "Games"},
		}, nil
	}}, nil)
	for _, tc := range []struct {
		name, path, want, absent string
		handler                  gin.HandlerFunc
	}{
		{name: "document", path: "/document", want: `"Page"`, absent: `"Variables"`, handler: h.publicPageDocument},
		{name: "values", path: "/values", want: `"event.name"`, absent: `"Page"`, handler: h.publicPageValues},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(w)
			ctx.Request = httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/content/pages/rules"+tc.path, nil)
			ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}, {Key: "slug", Value: "rules"}}
			tc.handler(ctx)
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), tc.absent) {
				t.Fatalf("unexpected %s response: %d %s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

func TestPublicPageAccessChecksCurrentVisibilityWithoutContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	checked := false
	h := NewEventSelfAPIHandler(fakeUseCase{pageAccess: func(_ context.Context, gotEventID uuid.UUID, slug string) error {
		checked = true
		if gotEventID != eventID || slug != "rules" {
			t.Fatalf("unexpected page access: event=%s slug=%q", gotEventID, slug)
		}
		return nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/content/pages/rules/access", nil)
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}, {Key: "slug", Value: "rules"}}
	h.publicPageAccess(ctx)
	if !checked || w.Code != http.StatusNoContent || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected access response: checked=%v code=%d headers=%v", checked, w.Code, w.Header())
	}
}

func TestLiveResultsStreamsChangesAfterSnapshotRevision(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	ctxRequest, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := NewEventSelfAPIHandler(fakeUseCase{live: func(_ context.Context, gotEventID uuid.UUID, access eventUseCase.ResultsAccess, after int64) (eventUseCase.LiveResultsReplay, error) {
		if gotEventID != eventID || after != 3 || access.UserID != nil {
			t.Fatalf("unexpected live arguments: event=%s after=%d access=%#v", gotEventID, after, access)
		}
		cancel()
		return eventUseCase.LiveResultsReplay{Changes: []eventUseCase.LiveResultChangeView{{Revision: 4, Kind: "team_challenge_solved"}}}, nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/results/live", nil).WithContext(ctxRequest)
	request.Header.Set("Last-Event-ID", "3")
	ctx.Request = request
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}

	h.liveResults(ctx)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "event: result-change") || !strings.Contains(w.Body.String(), "id: 4") {
		t.Fatalf("unexpected stream: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestLivePollIntervalUsesBoundedOptionalSeconds(t *testing.T) {
	for _, test := range []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{value: "", want: 2 * time.Second, ok: true},
		{value: "5", want: 5 * time.Second, ok: true},
		{value: "30", want: 30 * time.Second, ok: true},
		{value: "1"},
		{value: "31"},
		{value: "fast"},
	} {
		got, err := livePollInterval(test.value)
		if (err == nil) != test.ok || (test.ok && got != test.want) {
			t.Fatalf("livePollInterval(%q) = %s, %v", test.value, got, err)
		}
	}
}
func (fakeUseCase) GetOwnStandStatus(context.Context, uuid.UUID, uuid.UUID) (eventStandModel.Status, error) {
	return eventStandModel.StatusCreating, nil
}
func (fakeUseCase) GetOwnChallengeLabStatus(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{}, nil
}
func (f fakeUseCase) GetOwnLabVPNConfig(ctx context.Context, eventID, userID uuid.UUID) (string, error) {
	if f.vpnConfig != nil {
		return f.vpnConfig(ctx, eventID, userID)
	}
	return "", nil
}
func (f fakeUseCase) GetOwnVPNProbeStatus(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.VPNProbeStatusView, error) {
	if f.vpnStatus != nil {
		return f.vpnStatus(ctx, eventID, userID)
	}
	return eventUseCase.VPNProbeStatusView{}, nil
}

func TestPersonalVPNProbeStatusResponseIsPrivateAndScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{vpnStatus: func(_ context.Context, gotEvent, gotUser uuid.UUID) (eventUseCase.VPNProbeStatusView, error) {
		if gotEvent != eventID || gotUser != userID {
			t.Fatalf("wrong VPN identity: %s %s", gotEvent, gotUser)
		}
		return eventUseCase.VPNProbeStatusView{GatewayIP: "10.8.7.1", ProbeURL: "http://10.8.7.1:8088/"}, nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/teams/labs/vpn/status", nil)
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	h.labVPNStatus(ctx)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"GatewayIP":"10.8.7.1"`) || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unsafe VPN status: %d %s cache=%q", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
	}
}

func TestPersonalVPNConfigResponseIsPrivateAndScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{vpnConfig: func(_ context.Context, gotEvent, gotUser uuid.UUID) (string, error) {
		if gotEvent != eventID || gotUser != userID {
			t.Fatalf("wrong VPN identity: %s %s", gotEvent, gotUser)
		}
		return "private-personal-config", nil
	}}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/teams/labs/vpn", nil)
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	h.labVPNConfig(ctx)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Config":"private-personal-config"`) || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unsafe VPN response: %d %s cache=%q", w.Code, w.Body.String(), w.Header().Get("Cache-Control"))
	}
}

func TestCreateTeam_UsesExplicitEventIDAndSessionUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	called := false
	h := NewEventSelfAPIHandler(fakeUseCase{
		createTeam: func(_ context.Context, gotEventID, gotUserID uuid.UUID, name string) error {
			called = true
			if gotEventID != eventID || gotUserID != userID || name != "Blue Team" {
				t.Fatalf("unexpected create arguments: event=%s user=%s name=%q", gotEventID, gotUserID, name)
			}
			return nil
		},
	}, nil)

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	request := httptest.NewRequest(http.MethodPost, "/api/events/"+eventID.String()+"/teams", strings.NewReader(`{"Name":"Blue Team"}`))
	request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	ctx.Request = request
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}

	h.createTeam(ctx)

	if !called {
		t.Fatal("CreateTeam use case was not called")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, http.StatusOK, w.Body.String())
	}
}

type allowAllProtection struct{}

func (allowAllProtection) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.Next() }
}

func TestParticipantsCannotDeployOrReconcileStands(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewEventSelfAPIHandler(fakeUseCase{}, allowAllProtection{}).Init(router.Group("/api"), func(ctx *gin.Context) { ctx.Next() })
	eventID, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, action := range []string{"deploy", "reconcile"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/events/"+eventID.String()+"/teams/challenges/"+challengeID.String()+"/lab/"+action, nil))
		if w.Code != http.StatusNotFound {
			t.Fatalf("participant lab %s route answered %d; stands are managed by moderators only", action, w.Code)
		}
	}
}

func TestOwnStandStatusIsReadOnlyStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	h := NewEventSelfAPIHandler(fakeUseCase{}, nil)
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Params = gin.Params{{Key: "id", Value: eventID.String()}}
	request := httptest.NewRequest(http.MethodGet, "/api/events/"+eventID.String()+"/teams/labs/stand", nil)
	ctx.Request = request.WithContext(rbac.ContextWithCurrentUserSession(request.Context(), rbac.Claims{UserID: userID}))
	h.standStatus(ctx)
	if w.Code != http.StatusOK || w.Body.String() == "" || !strings.Contains(w.Body.String(), `"Status":"creating"`) || strings.Contains(w.Body.String(), "Reason") {
		t.Fatalf("stand status response: %d %s", w.Code, w.Body.String())
	}
}

func (fakeUseCase) GetOwnParticipantFormAnswers(context.Context, uuid.UUID, uuid.UUID) (map[string]any, error) {
	return map[string]any{}, nil
}

func (f fakeUseCase) GetOwnChallengeRuntime(ctx context.Context, eventID, userID, challengeID uuid.UUID) (eventUseCase.ChallengeRuntimeView, error) {
	status, err := f.GetOwnChallengeLabStatus(ctx, eventID, userID, challengeID)
	return eventUseCase.ChallengeRuntimeView{Status: status}, err
}
func (f fakeUseCase) GetOwnLabLifecycle(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (eventUseCase.ParticipantLabView, error) {
	return eventUseCase.ParticipantLabView{}, nil
}
