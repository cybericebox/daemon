// Package errorJournal is the HTTP delivery of the platform error journal: the grouped errors with their samples,
// the live stream, the 404 statistics and the notification settings. Super admins only (platform.errors.*). No
// client address is ever returned, and every message was scrubbed when it was recorded.
package errorJournal

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/errjournal"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/sse"
	errorJournalModel "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/model/rbac"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
)

const (
	// maxStreamsPerUser caps the live streams one account keeps open.
	maxStreamsPerUser = 3
	// defaultNotFoundDays is the 404 statistics period when the caller names none.
	defaultNotFoundDays = 30
)

type (
	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		ListErrorGroups(ctx context.Context, f errorJournalUseCase.GroupFilter) ([]errorJournalModel.Group, int64, error)
		GetErrorGroup(ctx context.Context, id uuid.UUID) (errorJournalUseCase.GroupDetail, error)
		SetErrorGroupStatus(ctx context.Context, id uuid.UUID, status errorJournalModel.Status) (errorJournalModel.Group, error)
		ErrorNotFoundStats(ctx context.Context, from, to time.Time) ([]errorJournalModel.NotFoundDay, error)
		GetErrorJournalSettings(ctx context.Context) (errorJournalUseCase.SettingsView, error)
		SaveErrorJournalSettings(ctx context.Context, in errorJournalUseCase.SettingsInput) (errorJournalUseCase.SettingsView, error)
		SendErrorJournalTest(ctx context.Context) ([]errorJournalUseCase.TestResult, error)
		ErrorJournalStream() (<-chan errorJournalUseCase.StreamEvent, func())
	}

	Handler struct {
		useCase IUseCase
		prot    IProtection
	}
)

func New(useCase IUseCase, prot IProtection) *Handler { return &Handler{useCase: useCase, prot: prot} }

func (h *Handler) Init(r *gin.RouterGroup) {
	g := r.Group("admin/errors")
	read := h.prot.RequirePermission(rbac.PermPlatformErrorsRead)
	write := h.prot.RequirePermission(rbac.PermPlatformErrorsWrite)
	g.GET("", read, h.list)
	g.GET("stream", read, h.stream)
	g.GET("not-found", read, h.notFound)
	g.GET("settings", read, h.getSettings)
	g.PUT("settings", write, h.putSettings)
	g.POST("settings/test", write, h.sendTest)
	g.GET(":groupID", read, h.get)
	g.PATCH(":groupID/status", write, h.setStatus)
}

// ── DTOs ────────────────────────────────────────────────────────────────

type (
	groupResponse struct {
		ID             uuid.UUID  `json:"ID"`
		Kind           string     `json:"Kind"`
		Source         string     `json:"Source"`
		Title          string     `json:"Title"`
		Status         string     `json:"Status"`
		Occurrences    int64      `json:"Occurrences"`
		FirstSeenAt    time.Time  `json:"FirstSeenAt"`
		LastSeenAt     time.Time  `json:"LastSeenAt"`
		ResolvedAt     *time.Time `json:"ResolvedAt"`
		LastNotifiedAt *time.Time `json:"LastNotifiedAt"`
	}

	sampleResponse struct {
		ID         uuid.UUID         `json:"ID"`
		OccurredAt time.Time         `json:"OccurredAt"`
		Message    string            `json:"Message"`
		Stack      string            `json:"Stack"`
		Method     string            `json:"Method"`
		Route      string            `json:"Route"`
		HTTPStatus *int              `json:"HTTPStatus"`
		RequestID  string            `json:"RequestID"`
		UserID     *uuid.UUID        `json:"UserID"`
		Role       string            `json:"Role"`
		Permission string            `json:"Permission"`
		Limiter    string            `json:"Limiter"`
		Details    map[string]string `json:"Details"`
	}

	listResponse struct {
		Items  []groupResponse `json:"Items"`
		Total  int64           `json:"Total"`
		Limit  int             `json:"Limit"`
		Offset int             `json:"Offset"`
	}

	detailResponse struct {
		Group   groupResponse    `json:"Group"`
		Samples []sampleResponse `json:"Samples"`
	}

	statusRequest struct {
		// Status is open, resolved or ignored.
		Status string `json:"Status" binding:"required"`
	}

	notFoundDayResponse struct {
		// Day is the calendar day (UTC), YYYY-MM-DD.
		Day string `json:"Day"`
		// Route is the route template of a handler 404; empty counts every unmatched path together.
		Route string `json:"Route"`
		Hits  int64  `json:"Hits"`
	}

	notFoundRouteResponse struct {
		Route string `json:"Route"`
		Hits  int64  `json:"Hits"`
	}

	notFoundResponse struct {
		From time.Time `json:"From"`
		To   time.Time `json:"To"`
		// Days has one row per day and route.
		Days []notFoundDayResponse `json:"Days"`
		// Routes has the totals per route over the period, the busiest first.
		Routes []notFoundRouteResponse `json:"Routes"`
		Total  int64                   `json:"Total"`
	}

	telegramChatResponse struct {
		ChatID       string     `json:"ChatID"`
		Label        string     `json:"Label"`
		Failing      bool       `json:"Failing"`
		FailingSince *time.Time `json:"FailingSince"`
		LastError    string     `json:"LastError"`
	}

	settingsResponse struct {
		Emails             []string               `json:"Emails"`
		EmailToSuperAdmins bool                   `json:"EmailToSuperAdmins"`
		TelegramEnabled    bool                   `json:"TelegramEnabled"`
		TelegramChats      []telegramChatResponse `json:"TelegramChats"`
		UpdatedAt          time.Time              `json:"UpdatedAt"`
	}

	chatRequest struct {
		ChatID string `json:"ChatID"`
		Label  string `json:"Label"`
	}

	settingsRequest struct {
		Emails             []string      `json:"Emails"`
		EmailToSuperAdmins bool          `json:"EmailToSuperAdmins"`
		TelegramChats      []chatRequest `json:"TelegramChats"`
	}

	testResultResponse struct {
		// Channel is telegram or email.
		Channel string `json:"Channel"`
		Target  string `json:"Target"`
		Label   string `json:"Label"`
		OK      bool   `json:"OK"`
		Error   string `json:"Error"`
	}

	testResponse struct {
		Results []testResultResponse `json:"Results"`
	}

	// streamEvent is the data of a live "error-group" event.
	streamEvent struct {
		Group  groupResponse   `json:"Group"`
		Sample *sampleResponse `json:"Sample"`
		// New: the fingerprint was first seen now, or came back after it was resolved.
		New bool `json:"New"`
	}
)

func groupOf(g errorJournalModel.Group) groupResponse {
	return groupResponse{
		ID: g.ID, Kind: string(g.Kind), Source: g.Source, Title: g.Title, Status: string(g.Status),
		Occurrences: g.Occurrences, FirstSeenAt: g.FirstSeenAt, LastSeenAt: g.LastSeenAt, ResolvedAt: g.ResolvedAt,
		LastNotifiedAt: g.LastNotifiedAt,
	}
}

func sampleOf(s errorJournalModel.Sample) sampleResponse {
	details := s.Details
	if details == nil {
		details = map[string]string{}
	}
	return sampleResponse{
		ID: s.ID, OccurredAt: s.OccurredAt, Message: s.Message, Stack: s.Stack, Method: s.Method, Route: s.Route,
		HTTPStatus: s.HTTPStatus, RequestID: s.RequestID, UserID: s.UserID, Role: s.Role, Permission: s.Permission,
		Limiter: s.Limiter, Details: details,
	}
}

func settingsOf(v errorJournalUseCase.SettingsView) settingsResponse {
	out := settingsResponse{
		Emails: append([]string{}, v.Emails...), EmailToSuperAdmins: v.EmailToSuperAdmins, TelegramEnabled: v.TelegramEnabled,
		TelegramChats: make([]telegramChatResponse, 0, len(v.TelegramChats)), UpdatedAt: v.UpdatedAt,
	}
	for _, c := range v.TelegramChats {
		out.TelegramChats = append(out.TelegramChats, telegramChatResponse{
			ChatID: c.ChatID, Label: c.Label, Failing: c.Failing, FailingSince: c.FailingSince, LastError: c.LastError,
		})
	}
	return out
}

// ── handlers ────────────────────────────────────────────────────────────

// list godoc
// @Summary  List error groups (super admin)
// @Tags     error-journal
// @Produce  json
// @Param    kind    query  []string  false  "kinds (repeat or comma separated): http_5xx, panic, http_403, http_429, job, queue, mail, lab_agent_offline, lab_deploy, lab_cert_expiry, lab_component"
// @Param    status  query  string    false  "open, resolved or ignored"
// @Param    from    query  string    false  "last seen at or after (RFC 3339)"
// @Param    to      query  string    false  "last seen at or before (RFC 3339)"
// @Param    q       query  string    false  "text in the title or source"
// @Param    limit   query  int       false  "page size, 1-200 (default 50)"
// @Param    offset  query  int       false  "offset"
// @Success  200  {object}  response.Response{data=listResponse}
// @Failure  403  {object}  response.Response
// @Router   /admin/errors [get]
func (h *Handler) list(c *gin.Context) {
	f := errorJournalUseCase.GroupFilter{Status: errorJournalModel.Status(c.Query("status")), Query: c.Query("q")}
	for _, raw := range c.QueryArray("kind") {
		for _, k := range strings.Split(raw, ",") {
			if k = strings.TrimSpace(k); k != "" {
				f.Kinds = append(f.Kinds, errorJournalModel.Kind(k))
			}
		}
	}
	var ok bool
	if f.From, ok = timeParam(c, "from"); !ok {
		return
	}
	if f.To, ok = timeParam(c, "to"); !ok {
		return
	}
	if f.Limit, ok = intParam(c, "limit"); !ok {
		return
	}
	if f.Offset, ok = intParam(c, "offset"); !ok {
		return
	}
	groups, total, err := h.useCase.ListErrorGroups(c, f)
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	limit := f.Limit
	if limit < 1 || limit > 200 {
		limit = 50
	}
	items := make([]groupResponse, 0, len(groups))
	for _, g := range groups {
		items = append(items, groupOf(g))
	}
	response.AbortWithData(c, listResponse{Items: items, Total: total, Limit: limit, Offset: f.Offset})
}

// get godoc
// @Summary  One error group with its recent samples (super admin)
// @Tags     error-journal
// @Produce  json
// @Param    groupID  path  string  true  "group id"
// @Success  200  {object}  response.Response{data=detailResponse}
// @Failure  404  {object}  response.Response
// @Router   /admin/errors/{groupID} [get]
func (h *Handler) get(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	detail, err := h.useCase.GetErrorGroup(c, id)
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	out := detailResponse{Group: groupOf(detail.Group), Samples: make([]sampleResponse, 0, len(detail.Samples))}
	for _, s := range detail.Samples {
		out.Samples = append(out.Samples, sampleOf(s))
	}
	response.AbortWithData(c, out)
}

// setStatus godoc
// @Summary  Resolve, ignore or reopen an error group (super admin)
// @Tags     error-journal
// @Accept   json
// @Produce  json
// @Param    groupID  path  string         true  "group id"
// @Param    body     body  statusRequest  true  "new status"
// @Success  200  {object}  response.Response{data=groupResponse}
// @Failure  404  {object}  response.Response
// @Router   /admin/errors/{groupID}/status [patch]
func (h *Handler) setStatus(c *gin.Context) {
	id, ok := groupID(c)
	if !ok {
		return
	}
	var req statusRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(c, err)
		return
	}
	g, err := h.useCase.SetErrorGroupStatus(c, id, errorJournalModel.Status(req.Status))
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	response.AbortWithData(c, groupOf(g))
}

// notFound godoc
// @Summary  Daily 404 statistics (super admin)
// @Description A handler 404 is counted per route template; every unmatched path (bots scanning) is one counter with an empty route. Paths are never stored.
// @Tags     error-journal
// @Produce  json
// @Param    from  query  string  false  "first day (RFC 3339), default 30 days back"
// @Param    to    query  string  false  "last day (RFC 3339), default today"
// @Success  200  {object}  response.Response{data=notFoundResponse}
// @Router   /admin/errors/not-found [get]
func (h *Handler) notFound(c *gin.Context) {
	now := time.Now().UTC()
	from, to := now.AddDate(0, 0, -defaultNotFoundDays), now
	if v, good := timeParam(c, "from"); !good {
		return
	} else if v != nil {
		from = *v
	}
	if v, good := timeParam(c, "to"); !good {
		return
	} else if v != nil {
		to = *v
	}
	rows, err := h.useCase.ErrorNotFoundStats(c, from, to)
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	out := notFoundResponse{From: from, To: to, Days: make([]notFoundDayResponse, 0, len(rows)), Routes: []notFoundRouteResponse{}}
	totals := map[string]int64{}
	for _, r := range rows {
		out.Days = append(out.Days, notFoundDayResponse{Day: r.Day.UTC().Format("2006-01-02"), Route: r.Route, Hits: r.Hits})
		totals[r.Route] += r.Hits
		out.Total += r.Hits
	}
	for route, hits := range totals {
		out.Routes = append(out.Routes, notFoundRouteResponse{Route: route, Hits: hits})
	}
	sort.Slice(out.Routes, func(i, j int) bool {
		if out.Routes[i].Hits != out.Routes[j].Hits {
			return out.Routes[i].Hits > out.Routes[j].Hits
		}
		return out.Routes[i].Route < out.Routes[j].Route
	})
	response.AbortWithData(c, out)
}

// getSettings godoc
// @Summary  Notification settings: e-mail list and Telegram chat ids (super admin)
// @Tags     error-journal
// @Produce  json
// @Success  200  {object}  response.Response{data=settingsResponse}
// @Router   /admin/errors/settings [get]
func (h *Handler) getSettings(c *gin.Context) {
	view, err := h.useCase.GetErrorJournalSettings(c)
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	response.AbortWithData(c, settingsOf(view))
}

// putSettings godoc
// @Summary  Replace the notification settings (super admin)
// @Description The lists replace the stored ones. A chat id that stays keeps its failing mark; the server owns the mark.
// @Tags     error-journal
// @Accept   json
// @Produce  json
// @Param    body  body  settingsRequest  true  "settings"
// @Success  200  {object}  response.Response{data=settingsResponse}
// @Router   /admin/errors/settings [put]
func (h *Handler) putSettings(c *gin.Context) {
	var req settingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(c, err)
		return
	}
	in := errorJournalUseCase.SettingsInput{Emails: req.Emails, EmailToSuperAdmins: req.EmailToSuperAdmins}
	for _, chat := range req.TelegramChats {
		in.TelegramChats = append(in.TelegramChats, errorJournalUseCase.ChatInput{ChatID: chat.ChatID, Label: chat.Label})
	}
	view, err := h.useCase.SaveErrorJournalSettings(c, in)
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	response.AbortWithData(c, settingsOf(view))
}

// sendTest godoc
// @Summary  Send a test message to every chat id and address (super admin)
// @Description Reports each target. A chat that answers is no longer marked failing; one the bot may not write to is marked failing.
// @Tags     error-journal
// @Produce  json
// @Success  200  {object}  response.Response{data=testResponse}
// @Router   /admin/errors/settings/test [post]
func (h *Handler) sendTest(c *gin.Context) {
	results, err := h.useCase.SendErrorJournalTest(c)
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	out := testResponse{Results: make([]testResultResponse, 0, len(results))}
	for _, r := range results {
		out.Results = append(out.Results, testResultResponse{Channel: r.Channel, Target: r.Target, Label: r.Label, OK: r.OK, Error: r.Error})
	}
	response.AbortWithData(c, out)
}

// stream godoc
// @Summary  Live stream of recorded errors (super admin, Server-Sent Events)
// @Description Sends an "error-group" event for every recorded occurrence (data: streamEvent) and a "heartbeat" event while idle. Nothing is replayed: fetch the list on connect.
// @Tags     error-journal
// @Produce  text/event-stream
// @Success  200  {object}  streamEvent
// @Failure  429  {object}  response.Response
// @Router   /admin/errors/stream [get]
func (h *Handler) stream(c *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(c.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(c)
		return
	}
	release, admitted := sse.Streams.Acquire("errors:"+claims.UserID.String(), maxStreamsPerUser)
	if !admitted {
		errjournal.SetLimiter(c, "error-journal-streams")
		response.AbortWithTooManyRequests(c)
		return
	}
	defer release()
	flusher, streamCtx, cancel, err := sse.Open(c)
	if err != nil {
		response.AbortWithError(c, err)
		return
	}
	defer cancel()
	events, unsubscribe := h.useCase.ErrorJournalStream()
	defer unsubscribe()
	sse.WriteHeaders(c)
	heartbeat := time.NewTicker(sse.HeartbeatInterval)
	defer heartbeat.Stop()
	for {
		select {
		case <-streamCtx.Done():
			return
		case ev := <-events:
			out := streamEvent{Group: groupOf(ev.Group), New: ev.New}
			if ev.Sample != nil {
				s := sampleOf(*ev.Sample)
				out.Sample = &s
			}
			payload, _ := json.Marshal(out)
			_, _ = fmt.Fprintf(c.Writer, "event: error-group\ndata: %s\n\n", payload)
			flusher.Flush()
		case <-heartbeat.C:
			sse.Heartbeat(c.Writer, flusher)
		}
	}
}

// ── helpers ─────────────────────────────────────────────────────────────

func groupID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(c.Param("groupID"))
	if err != nil {
		response.AbortWithBadRequest(c, err)
		return uuid.Nil, false
	}
	return id, true
}

func timeParam(c *gin.Context, name string) (*time.Time, bool) {
	value := c.Query(name)
	if value == "" {
		return nil, true
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		response.AbortWithBadRequest(c, err)
		return nil, false
	}
	return &parsed, true
}

func intParam(c *gin.Context, name string) (int, bool) {
	value := c.Query(name)
	if value == "" {
		return 0, true
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		response.AbortWithBadRequest(c, err)
		return 0, false
	}
	return n, true
}
