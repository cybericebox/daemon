// Package eventself is the HTTP delivery layer for participant self-service
// event routes. Legacy info/join routes are served on the event's tenant
// subdomain, while team mutations deliberately use an explicit event ID: their
// authorization derives from the authenticated user and that event, never from
// the browser Origin or the domain from which the request happened to arrive.
package eventself

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/labview"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/sse"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventResultRepo"
	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type (
	Handler struct {
		useCase  IUseCase
		prot     IProtection
		presence presenceThrottle
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		GetEventInfo(ctx context.Context, eventID uuid.UUID) (eventUseCase.EventInfoView, error)
		GetApprovedParticipantInfo(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.ParticipantEventInfoView, error)
		RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error
		GetPublicEventContent(ctx context.Context, eventID uuid.UUID, access eventUseCase.ContentAccess) (eventUseCase.PublicEventContentView, error)
		ListNavigationPages(ctx context.Context, eventID uuid.UUID, access eventUseCase.ContentAccess) ([]eventUseCase.NavigationPageView, error)
		GetPublicEventPage(ctx context.Context, eventID uuid.UUID, slug string, access eventUseCase.ContentAccess) (eventUseCase.PublicEventPageView, error)
		RequirePublicEventPage(ctx context.Context, eventID uuid.UUID, slug string) error
		GetJoinInfo(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.JoinInfoView, error)
		GetParticipantForm(ctx context.Context, eventID uuid.UUID) (eventUseCase.ParticipantFormView, error)
		SubmitParticipantForm(ctx context.Context, eventID, userID uuid.UUID, in eventUseCase.SubmitParticipantFormInput) (eventUseCase.ParticipantFormAnswerView, error)
		GetOwnParticipantFormAnswers(ctx context.Context, eventID, userID uuid.UUID) (map[string]any, error)
		UploadAnswerFile(ctx context.Context, eventID, actorID uuid.UUID, scope eventFormModel.AnswerScope, fieldKey, name string, r io.Reader) (eventUseCase.AnswerFileView, error)
		StreamOwnAnswerFile(ctx context.Context, eventID, userID, fileID uuid.UUID) (io.ReadCloser, eventUseCase.AnswerFileView, error)
		ListPendingEventForms(ctx context.Context, eventID, userID uuid.UUID) ([]eventUseCase.PendingEventFormView, error)
		GetOwnEventForm(ctx context.Context, eventID, formID, userID uuid.UUID) (eventUseCase.EventFormView, error)
		SubmitEventFormResponse(ctx context.Context, eventID, formID, userID uuid.UUID, in eventUseCase.SubmitEventFormResponseInput) error
		JoinEvent(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.JoinInfoView, error)
		AcceptParticipantInvitation(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.JoinInfoView, error)
		DeclineParticipantInvitation(ctx context.Context, eventID, userID uuid.UUID) error
		SetOwnPseudonym(ctx context.Context, eventID, userID uuid.UUID, pseudonym *string) (eventUseCase.ParticipantNameView, error)
		UpdateOwnTeamFields(ctx context.Context, eventID, teamID, userID uuid.UUID, fields map[string]any) (eventUseCase.OwnTeamView, error)
		CreateTeam(ctx context.Context, eventID, userID uuid.UUID, name string) error
		CreateTeamWithFields(ctx context.Context, eventID, userID uuid.UUID, name string, fields map[string]any) error
		GetTeamFields(ctx context.Context, eventID uuid.UUID) (eventUseCase.ParticipantFormView, error)
		JoinTeam(ctx context.Context, eventID, userID uuid.UUID, joinCode string) error
		GetOwnTeam(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.OwnTeamView, error)
		LeaveTeam(ctx context.Context, eventID, userID uuid.UUID) error
		RenameTeam(ctx context.Context, eventID, teamID, userID uuid.UUID, name string) error
		RegenerateTeamJoinCode(ctx context.Context, eventID, teamID, userID uuid.UUID, expiry eventTeamModel.JoinCodeExpiry) error
		TransferTeamCaptaincy(ctx context.Context, eventID, teamID, captainID, newCaptainID uuid.UUID) error
		KickTeamMember(ctx context.Context, eventID, captainID, userID uuid.UUID) error
		DisbandTeam(ctx context.Context, eventID, teamID, captainID uuid.UUID) error
		SubmitChallenge(ctx context.Context, eventID, userID, challengeID uuid.UUID, in eventUseCase.SubmitChallengeInput) (eventUseCase.SubmitChallengeResult, error)
		UnlockHint(ctx context.Context, eventID, userID, challengeID, hintID uuid.UUID) (eventUseCase.OwnHintView, error)
		ListOwnChallenges(ctx context.Context, eventID, userID uuid.UUID) ([]eventUseCase.OwnChallengeView, error)
		StreamOwnChallengeAttachment(ctx context.Context, eventID, userID, challengeID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error)
		OpenOwnChallenge(ctx context.Context, eventID, userID, challengeID uuid.UUID) error
		ListChallengeSolves(ctx context.Context, eventID, userID, challengeID, cursor uuid.UUID, pageSize int) (eventUseCase.ChallengeSolvesPage, error)
		ListOwnTeamMembers(ctx context.Context, eventID, userID uuid.UUID) ([]eventUseCase.TeamRosterMemberView, error)
		GetOwnParticipantAnswers(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.OwnParticipantAnswersView, error)
		UpdateOwnParticipantAnswers(ctx context.Context, eventID, userID uuid.UUID, answers map[string]any) (eventUseCase.OwnParticipantAnswersView, error)
		GetLiveScreen(ctx context.Context, eventID uuid.UUID, token string) (eventUseCase.LiveScreenView, error)
		ResolveLiveScreenToken(ctx context.Context, eventID uuid.UUID, token string) error
		GetResultsSnapshot(ctx context.Context, eventID uuid.UUID, access eventUseCase.ResultsAccess, liveScreen bool) (eventUseCase.ResultsSnapshotView, error)
		OpenLiveResults(eventID uuid.UUID, access eventUseCase.ResultsAccess, liveScreen bool) eventUseCase.LiveResultsSubscription
		GetOwnTeamResults(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.OwnTeamResultsView, error)
		GetParticipationStats(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.ParticipationStatsView, error)
		GetModeratorsParticipationStats(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.ParticipationStatsView, error)
		GetOwnChallengeLabStatus(ctx context.Context, eventID, userID, challengeID uuid.UUID) (exerciseModel.LabDeployStatus, error)
		GetOwnLabVPNConfig(ctx context.Context, eventID, userID uuid.UUID) (string, error)
		GetOwnVPNProbeStatus(ctx context.Context, eventID, userID uuid.UUID) (eventUseCase.VPNProbeStatusView, error)
		GetOwnStandStatus(ctx context.Context, eventID, userID uuid.UUID) (eventStandModel.Status, error)
	}
)

func NewEventSelfAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

// Init registers public event identity/content reads and participant self-service
// routes. The tenant-scoped self routes resolve the event from Origin; routes
// with an explicit event ID apply their own read or mutation permissions.
func (h *Handler) Init(router *gin.RouterGroup, resolveTenant gin.HandlerFunc) {
	public := router.Group("events/self", h.prot.RequirePermission(rbac.PermEventContentRead), resolveTenant, h.touchPresence)
	public.GET("public-info", h.publicInfo)
	h.initLiveScreen(router, resolveTenant)

	self := router.Group("events/self", h.prot.RequirePermission(rbac.PermSelf), resolveTenant, h.touchPresence)
	{
		self.GET("info", h.info)
		self.GET("participant-info", h.participantInfo)
		self.GET("join/info", h.joinInfo)
		self.POST("join", h.join)
		// join/invitation/accept is the pre-/invite alias kept for older clients.
		self.POST("join/invitation/accept", h.acceptInvitation)
		self.POST("invitation/accept", h.acceptInvitation)
		self.POST("invitation/decline", h.declineInvitation)
		self.PUT("pseudonym", h.setPseudonym)
		self.GET("participant-form", h.participantForm)
		self.GET("team-fields", h.teamFields)
		self.POST("participant-form", h.submitParticipantForm)
		self.GET("participant-answers", h.participantAnswers)
		self.PUT("participant-answers", h.updateParticipantAnswers)
		self.POST("answer-files", h.uploadAnswerFile)
		self.GET("answer-files/:fileID", h.downloadAnswerFile)
	}

	teams := router.Group("events/:id/teams", h.prot.RequirePermission(rbac.PermSelf), h.touchPresence)
	{
		teams.POST("", h.createTeam)
		teams.POST("join", h.joinTeam)
		teams.GET("mine", h.ownTeam)
		teams.GET("mine/members", h.ownTeamMembers)
		teams.POST("mine/leave", h.leaveTeam)
		teams.POST("mine/form", h.formTeam)
		teams.PUT(":teamID", h.renameTeam)
		teams.PUT(":teamID/fields", h.updateTeamFields)
		teams.POST(":teamID/join-code", h.regenerateJoinCode)
		teams.POST(":teamID/captain", h.transferCaptain)
		teams.POST(":teamID/members/:userID/kick", h.kickMember)
		teams.DELETE(":teamID", h.disbandTeam)
		teams.POST("challenges/:challengeID/submit", h.submitChallenge)
		teams.POST("challenges/:challengeID/hints/:hintID/unlock", h.unlockHint)
		teams.GET("challenges/mine", h.listOwnChallenges)
		teams.GET("challenges/:challengeID/files/:fileID", h.downloadChallengeAttachment)
		teams.POST("challenges/:challengeID/open", h.openChallenge)
		teams.GET("challenges/:challengeID/solves", h.challengeSolves)
		// Stands are managed only by moderators (manage/labs); participants
		// read the lab status, their team stand status and their VPN config.
		teams.GET("challenges/:challengeID/lab", h.labStatus)
		teams.POST("challenges/:challengeID/lab/link", h.openLabLink)
		teams.GET("labs/stand", h.standStatus)
		teams.GET("labs/vpn", h.labVPNConfig)
		teams.GET("labs/vpn/status", h.labVPNStatus)
	}

	results := router.Group("events/:id/results", h.prot.RequirePermission(rbac.PermEventResultsRead))
	{
		results.GET("", h.resultsSnapshot)
		results.GET("live", h.liveResults)
		results.GET("changes", h.resultsChanges)
	}
	content := router.Group("events/:id/content", h.prot.RequirePermission(rbac.PermEventContentRead))
	{
		content.GET("", h.publicContent)
		content.GET("document", h.publicContentDocument)
		content.GET("values", h.publicContentValues)
		content.GET("pages", h.visiblePages)
		content.GET("pages/:slug", h.publicPage)
		content.GET("pages/:slug/access", h.publicPageAccess)
		content.GET("pages/:slug/document", h.publicPageDocument)
		content.GET("pages/:slug/values", h.publicPageValues)
	}
	teamResults := router.Group("events/:id/results", h.prot.RequirePermission(rbac.PermSelf), h.touchPresence)
	{
		teamResults.GET("team", h.ownResults)
		teamResults.GET("participation", h.participationStats)
	}
	forms := router.Group("events/:id/forms", h.prot.RequirePermission(rbac.PermSelf), h.touchPresence)
	{
		forms.GET("pending", h.pendingForms)
		forms.GET(":formID", h.getForm)
		forms.POST(":formID/response", h.submitFormResponse)
	}
}

// publicInfo godoc
// @Summary Read tenant event identity and theme; unpublished events require manager access
// @Tags events-self
// @Produce json
// @Success 200 {object} response.Response{data=publicEventInfoResponse}
// @Router /events/self/public-info [get]
func (h *Handler) publicInfo(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	v, err := h.useCase.GetEventInfo(ctx, tenant.EventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if v.Status == eventModel.LifecycleNotPublished || v.Status == eventModel.LifecycleWithdrawn {
		claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
		if !ok {
			response.AbortWithNotFound(ctx)
			return
		}
		if err := h.useCase.RequireReadEvent(ctx.Request.Context(), tenant.EventID, claims.UserID); err != nil {
			if errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
				response.AbortWithNotFound(ctx)
			} else {
				response.AbortWithError(ctx, err)
			}
			return
		}
	}
	response.AbortWithData(ctx, toPublicEventInfoResponse(v))
}

// pendingForms godoc
// @Summary List the caller's incomplete event form deliveries
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/forms/pending [get]
func (h *Handler) pendingForms(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	values, err := h.useCase.ListPendingEventForms(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, values)
}

// getForm godoc
// @Summary Get a form delivered to the caller
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param formID path string true "form ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/forms/{formID} [get]
func (h *Handler) getForm(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	formID, err := uuid.FromString(ctx.Param("formID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	value, err := h.useCase.GetOwnEventForm(ctx, eventID, formID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, value)
}

// submitFormResponse godoc
// @Summary Submit the caller's response to a delivered form version
// @Tags events-self
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param formID path string true "form ID"
// @Param body body submitEventFormResponseRequest true "response"
// @Success 200 {object} response.Response
// @Router /events/{id}/forms/{formID}/response [post]
func (h *Handler) submitFormResponse(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	formID, err := uuid.FromString(ctx.Param("formID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req submitEventFormResponseRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.SubmitEventFormResponse(ctx, eventID, formID, claims.UserID, req.toInput()); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{"ok": true})
}

// liveResults godoc
// @Summary Stream result changes after a fresh results snapshot
// @Description Load GET /events/{id}/results first. Send its Revision in Last-Event-ID (or the lastEventId query parameter, for EventSource) when opening this stream. After any interruption, load a fresh snapshot before reconnecting. A frozen viewer does not receive other teams' solves after the freeze; a change of the viewer's freeze state ends the stream with snapshot-required.
// @Tags events-self
// @Produce text/event-stream
// @Param id path string true "event ID"
// @Param Last-Event-ID header string false "Revision from a newly loaded results snapshot"
// @Param lastEventId query integer false "Revision, when the header cannot be set"
// @Param view query string false "live = the live screen (event managers only, 403 61218)"
// @Param pollInterval query integer false "Database polling interval in seconds (2-30, default 2)"
// @Success 200 {string} string "SSE result-change and snapshot-required events"
// @Router /events/{id}/results/live [get]
func (h *Handler) liveResults(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	cursor := ctx.GetHeader("Last-Event-ID")
	if cursor == "" {
		cursor = ctx.Query("lastEventId")
	}
	afterRevision, err := liveCursor(cursor)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	pollInterval, err := livePollInterval(ctx.Query("pollInterval"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	liveScreen, err := resultsView(ctx.Query("view"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	access := resultsAccess(ctx, eventID)
	flusher, streamCtx, cancel, err := sse.Open(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer cancel()
	// Each tick reads the event's shared results state; no per-viewer query.
	subscription := h.useCase.OpenLiveResults(eventID, access, liveScreen)
	replay, err := subscription.Replay(streamCtx, afterRevision)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	sse.WriteHeaders(ctx)

	freezeKey := replay.FreezeKey
	lastRevision := afterRevision
	if !writeLiveReplay(ctx, flusher, replay, &lastRevision) {
		return
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	heartbeat := time.NewTicker(sse.HeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-streamCtx.Done():
			return
		case <-ticker.C:
			replay, replayErr := subscription.Replay(streamCtx, lastRevision)
			if replayErr != nil {
				return
			}
			// The freeze started, ended or was opened: the client's snapshot
			// no longer matches what this viewer may see.
			if replay.FreezeKey != freezeKey {
				replay = eventUseCase.LiveResultsReplay{SnapshotRequired: true}
			}
			if !writeLiveReplay(ctx, flusher, replay, &lastRevision) {
				return
			}
		case <-heartbeat.C:
			sse.Heartbeat(ctx.Writer, flusher)
		}
	}
}

// resultsView parses the view query: empty = the results page, live = the
// live screen.
func resultsView(value string) (bool, error) {
	switch value {
	case "":
		return false, nil
	case "live":
		return true, nil
	default:
		return false, fmt.Errorf("view must be empty or live")
	}
}

func liveCursor(header string) (int64, error) {
	if header == "" {
		return 0, fmt.Errorf("Last-Event-ID is required; load a results snapshot before subscribing")
	}
	value, err := strconv.ParseInt(header, 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("Last-Event-ID must be a non-negative revision")
	}
	return value, nil
}

func livePollInterval(value string) (time.Duration, error) {
	if value == "" {
		return 2 * time.Second, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 2 || seconds > 30 {
		return 0, fmt.Errorf("pollInterval must be between 2 and 30 seconds")
	}
	return time.Duration(seconds) * time.Second, nil
}

func writeLiveReplay(ctx *gin.Context, flusher http.Flusher, replay eventUseCase.LiveResultsReplay, lastRevision *int64) bool {
	if replay.SnapshotRequired {
		_, _ = fmt.Fprint(ctx.Writer, "event: snapshot-required\ndata: {}\n\n")
		flusher.Flush()
		return false
	}
	for _, change := range replay.Changes {
		if change.Kind == string(eventResultRepo.ChangeScoreboardRecalculated) {
			_, _ = fmt.Fprint(ctx.Writer, "event: snapshot-required\ndata: {}\n\n")
			flusher.Flush()
			return false
		}
		payload, marshalErr := json.Marshal(change)
		if marshalErr != nil {
			return false
		}
		_, _ = fmt.Fprintf(ctx.Writer, "event: result-change\nid: %d\ndata: %s\n\n", change.Revision, payload)
		*lastRevision = change.Revision
	}
	// Changes hidden by the freeze still advance the cursor.
	if replay.LastRevision > *lastRevision {
		*lastRevision = replay.LastRevision
	}
	flusher.Flush()
	return true
}

// resultsSnapshot godoc
// @Summary Get one visible event result snapshot
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param view query string false "live = the live screen: event managers only (403 61218), not cut to the page settings, frozen per LiveFreeze"
// @Success 200 {object} response.Response{data=resultsSnapshotResponse}
// @Router /events/{id}/results [get]
func (h *Handler) resultsSnapshot(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	liveScreen, err := resultsView(ctx.Query("view"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	access := resultsAccess(ctx, eventID)
	result, err := h.useCase.GetResultsSnapshot(ctx, eventID, access, liveScreen)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	// The results page polls: an unchanged snapshot answers 304.
	abortWithETagData(ctx, toResultsSnapshotResponse(result))
}

func teamIDFromPath(ctx *gin.Context) (uuid.UUID, bool) {
	teamID, err := uuid.FromString(ctx.Param("teamID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return teamID, true
}

func claimsFromContext(ctx *gin.Context) (rbac.Claims, bool) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
	}
	return claims, ok
}

func eventIDFromPath(ctx *gin.Context) (uuid.UUID, bool) {
	eventID, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return eventID, true
}

// submitChallenge godoc
// @Summary Submit an answer for an event challenge
// @Tags events-self
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param Idempotency-Key header string true "UUID generated once per submit action"
// @Param body body submitChallengeRequest true "answer"
// @Success 200 {object} response.Response{data=submitChallengeResponse}
// @Router /events/{id}/teams/challenges/{challengeID}/submit [post]
func (h *Handler) submitChallenge(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	var req submitChallengeRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	idempotencyKey, err := uuid.FromString(ctx.GetHeader("Idempotency-Key"))
	if err != nil || idempotencyKey == uuid.Nil {
		response.AbortWithError(ctx, challengeAttemptModel.ErrIdempotencyKeyRequired.Err())
		return
	}
	v, err := h.useCase.SubmitChallenge(ctx, eventID, claims.UserID, challengeID, eventUseCase.SubmitChallengeInput{Answer: req.Answer, IdempotencyKey: idempotencyKey, ReceivedAt: middleware.RequestReceivedAt(ctx.Request.Context())})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, submitChallengeResponse{Correct: v.Correct, FirstSolve: v.FirstSolve})
}

// unlockHint godoc
// @Summary Unlock one hint of a board challenge for the caller's team
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param hintID path string true "hint ID"
// @Success 200 {object} response.Response{data=ownHintResponse}
// @Router /events/{id}/teams/challenges/{challengeID}/hints/{hintID}/unlock [post]
func (h *Handler) unlockHint(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	hintID, err := uuid.FromString(ctx.Param("hintID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.UnlockHint(ctx, eventID, claims.UserID, challengeID, hintID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toOwnHintResponse(v))
}

// listOwnChallenges godoc
// @Summary List published challenges assigned to the caller's team
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]ownChallengeResponse}
// @Router /events/{id}/teams/challenges/mine [get]
func (h *Handler) listOwnChallenges(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListOwnChallenges(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]ownChallengeResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toOwnChallengeResponse(item))
	}
	response.AbortWithData(ctx, out)
}

// ownResults godoc
// @Summary Get the caller's current team results
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=ownResultsResponse}
// @Router /events/{id}/results/team [get]
func (h *Handler) ownResults(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	result, err := h.useCase.GetOwnTeamResults(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toOwnTeamResultsResponse(result))
}

// standStatus godoc
// @Summary Read the caller's team stand status (no failure details)
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=standStatusResponse}
// @Router /events/{id}/teams/labs/stand [get]
func (h *Handler) standStatus(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	status, err := h.useCase.GetOwnStandStatus(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, standStatusResponse{Status: status.String()})
}

// labStatus godoc
// @Summary Get safe runtime status for the caller's challenge lab
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Success 200 {object} response.Response{data=labStatusResponse}
// @Router /events/{id}/teams/challenges/{challengeID}/lab [get]
func (h *Handler) labStatus(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetOwnChallengeLabStatus(ctx, eventID, claims.UserID, challengeID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLabStatusResponse(v))
}

func toLabStatusResponse(v exerciseModel.LabDeployStatus) labStatusResponse {
	out := labStatusResponse{Phase: v.Phase, Ready: v.Ready, VPNCIDR: v.VPNCIDR, InternetCIDR: v.InternetCIDR, Access: make([]labAccessResponse, 0, len(v.Access)), Queue: labview.Queue(v.Queue)}
	for _, a := range v.Access {
		out.Access = append(out.Access, labAccessResponse{Device: a.Device, Port: a.Port, Protocol: a.Protocol, URL: a.URL})
	}
	return out
}

// labVPNConfig godoc
// @Summary Get the caller's personal event VPN configuration
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=labVPNConfigResponse}
// @Router /events/{id}/teams/labs/vpn [get]
func (h *Handler) labVPNConfig(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	config, err := h.useCase.GetOwnLabVPNConfig(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, labVPNConfigResponse{Config: config})
}

// labVPNStatus returns the tunnel-only test gateway for an approved team
// member. Its use case enforces the event VPN setting and team membership.
func (h *Handler) labVPNStatus(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	status, err := h.useCase.GetOwnVPNProbeStatus(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, labVPNStatusResponse{GatewayIP: status.GatewayIP, ProbeURL: status.ProbeURL})
}

// info godoc
// @Summary  Get the tenant event's participant-facing info
// @Tags     events-self
// @Produce  json
// @Success  200  {object}  response.Response{data=eventInfoResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/self/info [get]
func (h *Handler) info(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	v, err := h.useCase.GetEventInfo(ctx, tenant.EventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, toEventInfoResponse(v))
}

// participantInfo returns only capabilities granted to an approved participant
// of this event. The use case checks the caller's event-scoped approval.
func (h *Handler) participantInfo(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	v, err := h.useCase.GetApprovedParticipantInfo(ctx, tenant.EventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, toParticipantEventInfoResponse(v))
}

// joinInfo godoc
// @Summary  Get the caller's participation status for the tenant event
// @Tags     events-self
// @Produce  json
// @Success  200  {object}  response.Response{data=joinInfoResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/self/join/info [get]
func (h *Handler) joinInfo(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	v, err := h.useCase.GetJoinInfo(ctx, tenant.EventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toJoinInfoResponse(v))
}

// join godoc
// @Summary  Join the tenant event as the calling user
// @Tags     events-self
// @Produce  json
// @Success  200  {object}  response.Response{data=joinInfoResponse}
// @Failure  400  {object}  response.Response
// @Router   /events/self/join [post]
func (h *Handler) join(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	v, err := h.useCase.JoinEvent(ctx, tenant.EventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toJoinInfoResponse(v))
}

func (h *Handler) participantForm(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	v, err := h.useCase.GetParticipantForm(ctx, tenant.EventID)
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	answers, err := h.useCase.GetOwnParticipantFormAnswers(ctx, tenant.EventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, participantFormWithAnswersResponse{participantFormResponse: toParticipantFormResponse(v), Answers: participantVisibleAnswers(v.Document, answers)})
}

func (h *Handler) teamFields(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	v, err := h.useCase.GetTeamFields(ctx, tenant.EventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toParticipantFormResponse(v))
}

func (h *Handler) submitParticipantForm(ctx *gin.Context) {
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithNotFound(ctx)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req submitParticipantFormRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.SubmitParticipantForm(ctx, tenant.EventID, claims.UserID, eventUseCase.SubmitParticipantFormInput{Answers: req.Answers})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toParticipantFormAnswerResponse(v))
}

// createTeam godoc
// @Summary  Create a team in an explicitly selected event
// @Tags     events-self
// @Accept   json
// @Produce  json
// @Param    id    path  string             true  "event ID"
// @Param    body  body  createTeamRequest  true  "team name"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/teams [post]
func (h *Handler) createTeam(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req createTeamRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.CreateTeamWithFields(ctx, eventID, claims.UserID, req.Name, req.Fields); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// joinTeam godoc
// @Summary  Join a team in an explicitly selected event
// @Tags     events-self
// @Accept   json
// @Produce  json
// @Param    id    path  string           true  "event ID"
// @Param    body  body  joinTeamRequest  true  "team join code"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /events/{id}/teams/join [post]
func (h *Handler) joinTeam(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req joinTeamRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.JoinTeam(ctx, eventID, claims.UserID, req.JoinCode); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// ownTeam godoc
// @Summary Get the caller's team in an event
// @Tags events-self
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=ownTeamResponse}
// @Router /events/{id}/teams/mine [get]
func (h *Handler) ownTeam(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetOwnTeam(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toOwnTeamResponse(v))
}

// leaveTeam godoc
// @Summary Leave the caller's team
// @Tags events-self
// @Param id path string true "event ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/teams/mine/leave [post]
func (h *Handler) leaveTeam(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	if err := h.useCase.LeaveTeam(ctx, eventID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// renameTeam godoc
// @Summary Rename a captain-owned team
// @Tags events-self
// @Accept json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param body body updateTeamRequest true "new team name"
// @Success 200 {object} response.Response
// @Router /events/{id}/teams/{teamID} [put]
func (h *Handler) renameTeam(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	teamID, ok := teamIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	var req updateTeamRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.RenameTeam(ctx, eventID, teamID, claims.UserID, req.Name); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// regenerateJoinCode godoc
// @Summary Regenerate a captain-owned team join link, optionally with an expiry
// @Tags events-self
// @Accept json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param request body regenerateJoinCodeRequest false "expiry: none, day, week or start"
// @Success 200 {object} response.Response
// @Router /events/{id}/teams/{teamID}/join-code [post]
func (h *Handler) regenerateJoinCode(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	teamID, ok := teamIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	var req regenerateJoinCodeRequest
	if ctx.Request.ContentLength != 0 {
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
	}
	if err := h.useCase.RegenerateTeamJoinCode(ctx, eventID, teamID, claims.UserID, eventTeamModel.JoinCodeExpiry(req.Expiry)); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// transferCaptain godoc
// @Summary Transfer captaincy to a member of the same team
// @Tags events-self
// @Accept json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param body body transferCaptainRequest true "new captain"
// @Success 200 {object} response.Response
// @Router /events/{id}/teams/{teamID}/captain [post]
func (h *Handler) transferCaptain(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	teamID, ok := teamIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	var req transferCaptainRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.TransferTeamCaptaincy(ctx, eventID, teamID, claims.UserID, req.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// kickMember godoc
// @Summary Remove a member from a captain-owned team
// @Tags events-self
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param userID path string true "member user ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/teams/{teamID}/members/{userID}/kick [post]
func (h *Handler) kickMember(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	if err = h.useCase.KickTeamMember(ctx, eventID, claims.UserID, userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// disbandTeam godoc
// @Summary Disband a captain-owned team
// @Tags events-self
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/teams/{teamID} [delete]
func (h *Handler) disbandTeam(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	teamID, ok := teamIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DisbandTeam(ctx, eventID, teamID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// teamFormer is the optional «confirm the roster» capability of the use case,
// asserted at call time so the participant use case port and its fakes stay as
// they are.
type teamFormer interface {
	FormOwnTeam(ctx context.Context, eventID, captainID uuid.UUID) error
}

// formTeam godoc
// @Summary Confirm the caller's team roster («team formed»)
// @Description The captain closes the roster for good. From then on nobody joins or switches teams and the team gets its tasks. It needs the event minimum team size.
// @Tags events-self
// @Param id path string true "event ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/teams/mine/form [post]
func (h *Handler) formTeam(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	former, ok := h.useCase.(teamFormer)
	if !ok {
		response.AbortWithError(ctx, infraModel.ErrInfrastructureUnavailable.Err())
		return
	}
	if err := former.FormOwnTeam(ctx, eventID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
