package eventAnalytics

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// IntegrityUseCase is the «Доброчесність» part of IUseCase.
type IntegrityUseCase interface {
	GetEventAnalyticsIntegrity(ctx context.Context, eventID uuid.UUID, from, to *time.Time, th eventAnalyticsModel.IntegrityThresholds, filter eventAnalyticsUseCase.IntegrityFilter) (eventAnalyticsUseCase.IntegrityView, error)
	GetEventAnalyticsIntegrityFlags(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsUseCase.IntegrityFlag, error)
	ReviewEventSolve(ctx context.Context, eventID, teamChallengeID, reviewer uuid.UUID, note string) error
	UnreviewEventSolve(ctx context.Context, eventID, teamChallengeID uuid.UUID) error
	DismissIntegrityPattern(ctx context.Context, eventID, teamChallengeID, by uuid.UUID, scope eventAnalyticsModel.DismissScope, kind eventAnalyticsModel.IntegrityKind, key, note string) error
	ListIntegrityDismissals(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsUseCase.DismissalView, error)
	RemoveIntegrityDismissal(ctx context.Context, eventID, id uuid.UUID) error
}

// initIntegrity registers the «Доброчесність» routes. Every one is sensitive
// (other teams' names): owner, write moderators and platform admins only (§7).
func (h *Handler) initIntegrity(router *gin.RouterGroup) {
	analytics := router.Group("events/:id/manage/analytics", h.prot.RequirePermission(rbac.PermSelf))
	analytics.GET("integrity", h.requireSensitive, h.integrity)
	analytics.GET("integrity/export.csv", h.requireSensitive, h.exportIntegrity)
	analytics.GET("integrity/flags", h.requireSensitive, h.integrityFlags)
	analytics.PUT("integrity/solves/:teamChallengeID/review", h.requireSensitive, h.reviewSolve)
	analytics.DELETE("integrity/solves/:teamChallengeID/review", h.requireSensitive, h.unreviewSolve)
	analytics.GET("integrity/dismissals", h.requireSensitive, h.integrityDismissals)
	analytics.POST("integrity/dismissals", h.requireSensitive, h.dismissPattern)
	analytics.DELETE("integrity/dismissals/:dismissalID", h.requireSensitive, h.removeDismissal)
}

type (
	integrityResponse struct {
		Items []integrityItemResponse `json:"Items"`
		// Total counts the items matching the filter, before the response cap.
		Total int `json:"Total"`
		// Counts: flagged solves per signal kind (the filter apart from the
		// signal applies).
		Counts     map[string]int              `json:"Counts"`
		Thresholds integrityThresholdsResponse `json:"Thresholds"`
		Defaults   integrityThresholdsResponse `json:"Defaults"`
		Period     periodResponse              `json:"Period"`
	}

	// integrityItemResponse is a flagged solve. ChallengeID and TeamID give
	// the filters of the attempts journal; SolvedAt bounds its span.
	integrityItemResponse struct {
		TeamChallengeID uuid.UUID `json:"TeamChallengeID"`
		TeamID          uuid.UUID `json:"TeamID"`
		TeamName        string    `json:"TeamName"`
		ChallengeID     uuid.UUID `json:"ChallengeID"`
		ChallengeName   string    `json:"ChallengeName"`
		Level           string    `json:"Level"`
		// Solved is false for a task flagged before any solve (cross_flag); At
		// is then the last suspicious submission, otherwise the solve.
		Solved  bool                      `json:"Solved"`
		At      time.Time                 `json:"At"`
		Signals []integritySignalResponse `json:"Signals"`
		Review  *integrityReviewResponse  `json:"Review"`
	}

	// integritySignalResponse: Kind is no_access, no_lab, too_fast,
	// first_try_hard, shared_wrong, burst, brute_force or follows_solve. The
	// numbers depend on the kind (see eventAnalyticsModel.IntegritySignal).
	integritySignalResponse struct {
		Kind            string                  `json:"Kind"`
		Count           int                     `json:"Count"`
		Extra           int                     `json:"Extra"`
		Seconds         int64                   `json:"Seconds"`
		BaselineSeconds int64                   `json:"Baseline"`
		Teams           []integrityTeamResponse `json:"Teams"`
		// Info marks a low-weight signal (shared_wrong on a static flag).
		Info bool `json:"Info"`
		// Answers (shared_wrong): each shared value with who sent it first,
		// second, ...; the value is the key of a dismissal.
		Answers []integrityAnswerResponse `json:"Answers"`
		// Owner (cross_flag): whose flag and which task. At: the last
		// cross_flag submission, or the first knock of a no_lab whose lab never
		// answered (then Extra is 1).
		Owner *integrityOwnerResponse `json:"Owner"`
		At    *time.Time              `json:"At"`
	}

	integrityAnswerResponse struct {
		Value string                        `json:"Value"`
		Order []integritySubmissionResponse `json:"Order"`
	}

	integritySubmissionResponse struct {
		TeamID   uuid.UUID `json:"TeamID"`
		TeamName string    `json:"TeamName"`
		At       time.Time `json:"At"`
	}

	integrityOwnerResponse struct {
		TeamID        uuid.UUID `json:"TeamID"`
		TeamName      string    `json:"TeamName"`
		ChallengeID   uuid.UUID `json:"ChallengeID"`
		ChallengeName string    `json:"ChallengeName"`
		SameTask      bool      `json:"SameTask"`
	}

	integrityDismissalResponse struct {
		ID            uuid.UUID `json:"ID"`
		Scope         string    `json:"Scope"`
		Kind          string    `json:"Kind"`
		Key           string    `json:"Key"`
		Note          string    `json:"Note"`
		ChallengeName string    `json:"ChallengeName"`
		CreatedBy     string    `json:"CreatedBy"`
		CreatedAt     time.Time `json:"CreatedAt"`
	}

	dismissRequest struct {
		TeamChallengeID uuid.UUID `json:"TeamChallengeID"`
		Kind            string    `json:"Kind"`
		Key             string    `json:"Key"`
		Scope           string    `json:"Scope"`
		Note            string    `json:"Note"`
	}

	integrityTeamResponse struct {
		ID   uuid.UUID `json:"ID"`
		Name string    `json:"Name"`
	}

	integrityReviewResponse struct {
		Note       string    `json:"Note"`
		ReviewedBy string    `json:"ReviewedBy"`
		ReviewedAt time.Time `json:"ReviewedAt"`
	}

	// integrityThresholdsResponse: floors and windows are seconds, floors are
	// keyed by task level.
	integrityThresholdsResponse struct {
		FloorSeconds       map[string]int `json:"FloorSeconds"`
		BruteForceAttempts int            `json:"BruteForceAttempts"`
		BruteForceWindow   int            `json:"BruteForceWindowSeconds"`
		FollowGap          int            `json:"FollowGapSeconds"`
	}

	integrityFlagResponse struct {
		TeamChallengeID uuid.UUID `json:"TeamChallengeID"`
		TeamID          uuid.UUID `json:"TeamID"`
		ChallengeID     uuid.UUID `json:"ChallengeID"`
		Count           int       `json:"Count"`
		Signals         []string  `json:"Signals"`
		// CrossFlagTimes: the submissions of another team's flag, to mark
		// those attempts in the journal.
		CrossFlagTimes []time.Time `json:"CrossFlagTimes"`
	}

	reviewRequest struct {
		Note string `json:"Note"`
	}
)

// integrity godoc
// @Summary Flagged solves to review (no IP, no automatic action)
// @Description §6.6 «Доброчесність». A solve (or a task, for cross_flag) is flagged by signals computed on read: cross_flag (the team submitted another team's flag), no_access (no open, download or hint unlock before the flag), no_lab (a lab task without any VPN session), too_fast (below the level floor), first_try_hard, shared_wrong, burst, brute_force, follows_solve. Most signals first. Hidden teams are excluded. Sensitive.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Param signal query string false "only solves with this signal"
// @Param teamId query string false "only this team"
// @Param challengeId query string false "only this task"
// @Param reviewed query string false "yes or no; default both"
// @Param floorElementary query int false "shortest plausible time from the first open, seconds (0-3600, default 0 = off)"
// @Param floorTrivial query int false "same for trivial (default 5)"
// @Param floorEasy query int false "same for easy (default 20)"
// @Param floorMedium query int false "same for medium (default 60)"
// @Param floorHard query int false "same for hard (default 120)"
// @Param floorInsane query int false "same for insane (default 240)"
// @Param bruteForceAttempts query int false "attempts making a brute_force (3-1000, default 15)"
// @Param bruteForceWindow query int false "brute_force window, seconds (10-3600, default 60)"
// @Param followGap query int false "follows_solve gap, seconds (5-3600, default 60)"
// @Success 200 {object} response.Response{data=integrityResponse}
// @Router /events/{id}/manage/analytics/integrity [get]
func (h *Handler) integrity(ctx *gin.Context) {
	v, ok := h.loadIntegrity(ctx)
	if !ok {
		return
	}
	out := integrityResponse{
		Items: make([]integrityItemResponse, 0, len(v.Items)), Total: v.Total, Counts: make(map[string]int, len(v.Counts)),
		Thresholds: toThresholdsResponse(v.Thresholds), Defaults: toThresholdsResponse(v.Defaults),
		Period: periodResponse{From: v.Period.From, To: v.Period.To},
	}
	for _, kind := range eventAnalyticsModel.IntegrityKinds {
		out.Counts[string(kind)] = v.Counts[kind]
	}
	for _, item := range v.Items {
		signals := make([]integritySignalResponse, 0, len(item.Signals))
		for _, s := range item.Signals {
			teams := make([]integrityTeamResponse, 0, len(s.Teams))
			for _, t := range s.Teams {
				teams = append(teams, integrityTeamResponse{ID: t.ID, Name: t.Name})
			}
			resp := integritySignalResponse{
				Kind: string(s.Kind), Count: s.Count, Extra: s.Extra, Seconds: s.Seconds, BaselineSeconds: s.Baseline, Teams: teams,
				Info: s.Info, Answers: make([]integrityAnswerResponse, 0, len(s.Answers)),
			}
			for _, a := range s.Answers {
				order := make([]integritySubmissionResponse, 0, len(a.Order))
				for _, o := range a.Order {
					order = append(order, integritySubmissionResponse{TeamID: o.Team.ID, TeamName: o.Team.Name, At: o.At})
				}
				resp.Answers = append(resp.Answers, integrityAnswerResponse{Value: a.Value, Order: order})
			}
			if s.Owner != nil {
				resp.Owner = &integrityOwnerResponse{TeamID: s.Owner.Team.ID, TeamName: s.Owner.Team.Name,
					ChallengeID: s.Owner.ChallengeID, ChallengeName: s.Owner.ChallengeName, SameTask: s.Owner.SameTask}
			}
			if !s.At.IsZero() {
				at := s.At
				resp.At = &at
			}
			signals = append(signals, resp)
		}
		resp := integrityItemResponse{
			TeamChallengeID: item.TeamChallengeID, TeamID: item.Team.ID, TeamName: item.Team.Name,
			ChallengeID: item.ChallengeID, ChallengeName: item.ChallengeName, Level: item.Level, Solved: item.Solved, At: item.At,
			Signals: signals,
		}
		if item.Review != nil {
			resp.Review = &integrityReviewResponse{Note: item.Review.Note, ReviewedBy: item.Review.ReviewedBy, ReviewedAt: item.Review.ReviewedAt}
		}
		out.Items = append(out.Items, resp)
	}
	response.AbortWithData(ctx, out)
}

func (h *Handler) loadIntegrity(ctx *gin.Context) (eventAnalyticsUseCase.IntegrityView, bool) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return eventAnalyticsUseCase.IntegrityView{}, false
	}
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return eventAnalyticsUseCase.IntegrityView{}, false
	}
	th, ok := parseThresholds(ctx)
	if !ok {
		return eventAnalyticsUseCase.IntegrityView{}, false
	}
	filter, ok := parseIntegrityFilter(ctx)
	if !ok {
		return eventAnalyticsUseCase.IntegrityView{}, false
	}
	v, err := h.useCase.GetEventAnalyticsIntegrity(ctx, eventID, from, to, th, filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return eventAnalyticsUseCase.IntegrityView{}, false
	}
	return v, true
}

func toThresholdsResponse(t eventAnalyticsModel.IntegrityThresholds) integrityThresholdsResponse {
	floors := make(map[string]int, len(t.Floors))
	for level, floor := range t.Floors {
		floors[level] = int(floor / time.Second)
	}
	return integrityThresholdsResponse{
		FloorSeconds: floors, BruteForceAttempts: t.BruteForceAttempts,
		BruteForceWindow: int(t.BruteForceWindow / time.Second), FollowGap: int(t.FollowGap / time.Second),
	}
}

// floorParams maps a level to its query parameter.
var floorParams = map[string]string{
	"elementary": "floorElementary", "trivial": "floorTrivial", "easy": "floorEasy",
	"medium": "floorMedium", "hard": "floorHard", "insane": "floorInsane",
}

// parseThresholds reads the optional threshold parameters over the defaults;
// the use case clamps them.
func parseThresholds(ctx *gin.Context) (eventAnalyticsModel.IntegrityThresholds, bool) {
	th := eventAnalyticsModel.DefaultIntegrityThresholds()
	number := func(name string, set func(int)) bool {
		raw := ctx.Query(name)
		if raw == "" {
			return true
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return false
		}
		set(n)
		return true
	}
	seconds := func(target *time.Duration) func(int) {
		return func(n int) { *target = time.Duration(n) * time.Second }
	}
	for level, param := range floorParams {
		level := level
		if !number(param, func(n int) { th.Floors[level] = time.Duration(n) * time.Second }) {
			return th, false
		}
	}
	if !number("bruteForceAttempts", func(n int) { th.BruteForceAttempts = n }) ||
		!number("bruteForceWindow", seconds(&th.BruteForceWindow)) ||
		!number("followGap", seconds(&th.FollowGap)) {
		return th, false
	}
	return th, true
}

func parseIntegrityFilter(ctx *gin.Context) (eventAnalyticsUseCase.IntegrityFilter, bool) {
	var filter eventAnalyticsUseCase.IntegrityFilter
	if raw := ctx.Query("signal"); raw != "" {
		if !eventAnalyticsModel.ValidIntegrityKind(raw) {
			response.AbortWithBadRequest(ctx)
			return filter, false
		}
		filter.Signal = eventAnalyticsModel.IntegrityKind(raw)
	}
	for name, target := range map[string]*uuid.UUID{"teamId": &filter.TeamID, "challengeId": &filter.ChallengeID} {
		if raw := ctx.Query(name); raw != "" {
			id, err := uuid.FromString(raw)
			if err != nil {
				response.AbortWithBadRequest(ctx, err)
				return filter, false
			}
			*target = id
		}
	}
	switch raw := ctx.Query("reviewed"); raw {
	case "":
	case "yes", "no":
		filter.Reviewed = eventAnalyticsUseCase.ReviewedFilter(raw)
	default:
		response.AbortWithBadRequest(ctx)
		return filter, false
	}
	return filter, true
}

// integrityFlags godoc
// @Summary Unreviewed flagged solves for the attempts journal
// @Description A marker per flagged solve: which team and task, and the kinds of its signals. No evidence and no other team's data. Default thresholds, whole event. Sensitive.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]integrityFlagResponse}
// @Router /events/{id}/manage/analytics/integrity/flags [get]
func (h *Handler) integrityFlags(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	flags, err := h.useCase.GetEventAnalyticsIntegrityFlags(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]integrityFlagResponse, 0, len(flags))
	for _, f := range flags {
		kinds := make([]string, 0, len(f.Signals))
		for _, k := range f.Signals {
			kinds = append(kinds, string(k))
		}
		out = append(out, integrityFlagResponse{TeamChallengeID: f.TeamChallengeID, TeamID: f.TeamID, ChallengeID: f.ChallengeID, Count: len(kinds), Signals: kinds, CrossFlagTimes: append([]time.Time{}, f.CrossFlagTimes...)})
	}
	response.AbortWithData(ctx, out)
}

// reviewSolve godoc
// @Summary Mark a flagged solve as reviewed
// @Description Stores the organizer's note (up to 1000 characters) and their name. Sensitive.
// @Tags event-analytics
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param teamChallengeID path string true "team challenge ID of the solve"
// @Param request body reviewRequest true "note"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/analytics/integrity/solves/{teamChallengeID}/review [put]
func (h *Handler) reviewSolve(ctx *gin.Context) {
	eventID, teamChallengeID, ok := parseSolveIDs(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req reviewRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.ReviewEventSolve(ctx, eventID, teamChallengeID, claims.UserID, req.Note); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// unreviewSolve godoc
// @Summary Take the reviewed mark off a solve
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param teamChallengeID path string true "team challenge ID of the solve"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/analytics/integrity/solves/{teamChallengeID}/review [delete]
func (h *Handler) unreviewSolve(ctx *gin.Context) {
	eventID, teamChallengeID, ok := parseSolveIDs(ctx)
	if !ok {
		return
	}
	if err := h.useCase.UnreviewEventSolve(ctx, eventID, teamChallengeID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

func parseSolveIDs(ctx *gin.Context) (eventID, teamChallengeID uuid.UUID, ok bool) {
	if eventID, ok = parseEventID(ctx); !ok {
		return uuid.Nil, uuid.Nil, false
	}
	teamChallengeID, err := uuid.FromString(ctx.Param("teamChallengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, false
	}
	return eventID, teamChallengeID, true
}

// exportIntegrity godoc
// @Summary Flagged solves as CSV (UTF-8 with BOM)
// @Description Same filters and thresholds as the JSON route. Sensitive.
// @Tags event-analytics
// @Produce text/csv
// @Param id path string true "event ID"
// @Param from query string false "period start (RFC 3339)"
// @Param to query string false "period end, exclusive (RFC 3339)"
// @Success 200 {string} string "CSV"
// @Router /events/{id}/manage/analytics/integrity/export.csv [get]
func (h *Handler) exportIntegrity(ctx *gin.Context) {
	v, ok := h.loadIntegrity(ctx)
	if !ok {
		return
	}
	w := startAnalyticsCSV(ctx, "integrity")
	_ = w.Write([]string{"Команда", "Завдання", "Рівень", "Здано або підозра (UTC)", "Сигнали", "Перевірено", "Нотатка"})
	for _, item := range v.Items {
		kinds := make([]string, 0, len(item.Signals))
		for _, s := range item.Signals {
			kinds = append(kinds, string(s.Kind))
		}
		reviewed, note := "ні", ""
		if item.Review != nil {
			reviewed, note = "так", item.Review.Note
		}
		at := item.At
		_ = w.Write([]string{csvCellText(item.Team.Name), csvCellText(item.ChallengeName), item.Level, csvTimePtr(&at),
			strings.Join(kinds, "; "), reviewed, csvCellText(note)})
	}
	w.Flush()
}

// integrityDismissals godoc
// @Summary Dismissed integrity patterns that apply to the event
// @Description The event's own «не підсвічувати такі випадки» and those of the catalog exercises it uses. Sensitive.
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]integrityDismissalResponse}
// @Router /events/{id}/manage/analytics/integrity/dismissals [get]
func (h *Handler) integrityDismissals(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	list, err := h.useCase.ListIntegrityDismissals(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]integrityDismissalResponse, 0, len(list))
	for _, d := range list {
		out = append(out, integrityDismissalResponse{ID: d.ID, Scope: string(d.Scope), Kind: string(d.Kind), Key: d.Key, Note: d.Note,
			ChallengeName: d.ChallengeName, CreatedBy: d.CreatedBy, CreatedAt: d.CreatedAt})
	}
	response.AbortWithData(ctx, out)
}

// dismissPattern godoc
// @Summary Do not highlight such cases again
// @Description Dismisses a pattern (signal kind plus its key) for the catalog task behind a team task of the event. Scope event reaches this event, scope exercise every event using the catalog exercise (owner, write moderators and platform admins, like every sensitive route). Key is the wrong value for shared_wrong and empty otherwise. burst and cross_flag cannot be dismissed. Sensitive.
// @Tags event-analytics
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param request body dismissRequest true "pattern"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/analytics/integrity/dismissals [post]
func (h *Handler) dismissPattern(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req dismissRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.DismissIntegrityPattern(ctx, eventID, req.TeamChallengeID, claims.UserID,
		eventAnalyticsModel.DismissScope(req.Scope), eventAnalyticsModel.IntegrityKind(req.Kind), req.Key, req.Note); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// removeDismissal godoc
// @Summary Take a dismissal back
// @Tags event-analytics
// @Produce json
// @Param id path string true "event ID"
// @Param dismissalID path string true "dismissal ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/analytics/integrity/dismissals/{dismissalID} [delete]
func (h *Handler) removeDismissal(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	id, err := uuid.FromString(ctx.Param("dismissalID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.RemoveIntegrityDismissal(ctx, eventID, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
