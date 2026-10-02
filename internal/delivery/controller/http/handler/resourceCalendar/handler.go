// Package resourceCalendar is the HTTP delivery of the resource calendar: the platform admin's timeline,
// capacity, event reservations, change requests, readiness alarms and the test pool; the organizer's allocated
// vs used and change requests; the catalog author's test laboratory room and bookings.
package resourceCalendar

import (
	"context"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	calUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		RequireManageEvent(ctx context.Context, eventID, userID uuid.UUID) error
		RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error

		GetResourceCalendarTimeline(ctx context.Context, from, to time.Time) (calUseCase.TimelineView, error)
		GetResourceCalendarCapacity(ctx context.Context) (calUseCase.CapacityView, error)
		GetResourceCalendarStats(ctx context.Context) (calUseCase.StatsView, error)
		GetResourceCalendarSettings(ctx context.Context) (calModel.Settings, error)
		SetResourceTestPool(ctx context.Context, pool calUseCase.Amount, allowConflicts bool) (calModel.Settings, []calUseCase.ConflictView, error)
		GetEventResourceReservation(ctx context.Context, eventID uuid.UUID) (calUseCase.ReservationResult, error)
		SetEventResourceReservation(ctx context.Context, eventID uuid.UUID, in calUseCase.EventReservationInput, by uuid.UUID) (calUseCase.ReservationResult, error)
		DeleteEventResourceReservation(ctx context.Context, eventID, by uuid.UUID) error
		ReplanResourceReservation(ctx context.Context, id uuid.UUID, allowConflicts bool) (calUseCase.ReservationResult, error)
		ReconcileResourceCalendar(ctx context.Context) error
		ListResourceAlarms(ctx context.Context, onlyOpen bool) ([]calUseCase.AlarmView, error)
		AcknowledgeResourceAlarm(ctx context.Context, id, by uuid.UUID) (calUseCase.AlarmView, error)
		ListResourceChangeRequests(ctx context.Context, status *calModel.ChangeStatus, eventID *uuid.UUID) ([]calUseCase.ChangeRequestView, error)
		DecideResourceChangeRequest(ctx context.Context, id uuid.UUID, approve bool, note string, by uuid.UUID, allowConflicts bool) (calUseCase.ChangeRequestView, error)

		GetEventResources(ctx context.Context, eventID uuid.UUID) (calUseCase.OrganizerReservationView, error)
		RequestResourceChange(ctx context.Context, eventID, by uuid.UUID, in calUseCase.ChangeInput) (calUseCase.OrganizerChangeView, error)

		CheckTestLabRoom(ctx context.Context, owner uuid.UUID, size, device calUseCase.Amount, lease time.Duration) (calUseCase.TestLabRoom, error)
		BookTestLab(ctx context.Context, owner uuid.UUID, start time.Time, duration time.Duration, size, device calUseCase.Amount) (calUseCase.BookingView, error)
		ListTestLabBookings(ctx context.Context, owner uuid.UUID) ([]calUseCase.BookingView, error)
		CancelTestLabBooking(ctx context.Context, owner, id uuid.UUID) error
	}
)

func NewResourceCalendarAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	read := h.prot.RequirePermission(rbac.PermInfrastructureRead)
	write := h.prot.RequirePermission(rbac.PermInfrastructureWrite)
	cal := router.Group("infrastructure/calendar")
	{
		cal.GET("timeline", read, h.getTimeline)
		cal.GET("capacity", read, h.getCapacity)
		cal.GET("stats", read, h.getStats)
		cal.GET("settings", read, h.getSettings)
		cal.PUT("settings", write, h.setSettings)
		cal.POST("recheck", write, h.recheck)
		cal.GET("alarms", read, h.listAlarms)
		cal.POST("alarms/:alarmID/acknowledge", write, h.acknowledgeAlarm)
		cal.GET("change-requests", read, h.listChangeRequests)
		cal.POST("change-requests/:requestID/decision", write, h.decideChangeRequest)
		cal.GET("events/:eventID/reservation", read, h.getEventReservation)
		cal.PUT("events/:eventID/reservation", write, h.setEventReservation)
		cal.DELETE("events/:eventID/reservation", write, h.deleteEventReservation)
		cal.POST("reservations/:reservationID/replan", write, h.replan)
	}

	self := h.prot.RequirePermission(rbac.PermSelf)
	manage := router.Group("events/:id/manage/resources", self)
	{
		manage.GET("", h.requireRead, h.getEventResources)
		manage.POST("change-requests", h.requireManage, h.requestChange)
	}

	labs := router.Group("exercises/test-labs", self)
	{
		labs.GET("room", h.testLabRoom)
		labs.GET("bookings", h.listBookings)
		labs.POST("bookings", h.book)
		labs.DELETE("bookings/:bookingID", h.cancelBooking)
	}
}

func (h *Handler) requireManage(ctx *gin.Context) {
	h.requireEvent(ctx, func(c context.Context, eventID, userID uuid.UUID) error {
		return h.useCase.RequireManageEvent(c, eventID, userID)
	})
}

func (h *Handler) requireRead(ctx *gin.Context) {
	h.requireEvent(ctx, func(c context.Context, eventID, userID uuid.UUID) error {
		return h.useCase.RequireReadEvent(c, eventID, userID)
	})
}

func (h *Handler) requireEvent(ctx *gin.Context, check func(context.Context, uuid.UUID, uuid.UUID) error) {
	id, ok := uuidParam(ctx, "id")
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err := check(ctx, id, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
	}
}

func uuidParam(ctx *gin.Context, name string) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param(name))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

func caller(ctx *gin.Context) (uuid.UUID, bool) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return uuid.Nil, false
	}
	return claims.UserID, true
}

// getTimeline godoc
//
//	@Summary		Resource calendar timeline
//	@Description	The calendar over a range of 15-minute slots (at most 31 days): the reservations (events, test bookings) as rectangles with where they are placed, the total reserved over time, the capacity of the agents that are used (enabled, meeting the platform requirements, with a recorded capacity), the conflicts (an agent over capacity, a team without an agent, the guaranteed test pool that does not fit) and the open readiness alarms. Feasibility is checked by packing, never by adding free room across agents. Maintenance lists the maintenance windows the cluster operators announced on the agents (set on the cluster, never by the platform admin): in one the agent gives the platform no capacity, or what the window leaves, so reservations placed on it in the window are conflicts and raise agent_shrunk alarms, and new ones are placed elsewhere. MaintenanceReported is true when every agent that is used has reported its windows. Requires infrastructure.read (super_admin only).
//	@Tags			infrastructure
//	@Produce		json
//	@Param			from	query		string	true	"range start, RFC3339 (aligned down to a slot)"
//	@Param			to		query		string	true	"range end, RFC3339 (aligned up to a slot)"
//	@Success		200		{object}	response.Response{data=timelineDTO}
//	@Router			/infrastructure/calendar/timeline [get]
func (h *Handler) getTimeline(ctx *gin.Context) {
	from, err := time.Parse(time.RFC3339, ctx.Query("from"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	to, err := time.Parse(time.RFC3339, ctx.Query("to"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.GetResourceCalendarTimeline(ctx, from, to)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, timeline(v))
}

// getCapacity godoc
//
//	@Summary		Resource calendar capacity
//	@Description	The capacity of every agent as the calendar uses it: the recorded tenant quota (no quota is no limit on that resource), the device maximum, whether the agent is connected (its capacity was read recently) and, for an agent that is not used, why (disabled, below_requirements, no_capacity). PerNodeRoomReported is true when every agent that is used reports the room of its nodes, which a device must then fit one of (Nodes of each agent); an agent that reports none counts as one node. Requires infrastructure.read.
//	@Tags			infrastructure
//	@Produce		json
//	@Success		200	{object}	response.Response{data=capacityDTO}
//	@Router			/infrastructure/calendar/capacity [get]
func (h *Handler) getCapacity(ctx *gin.Context) {
	v, err := h.useCase.GetResourceCalendarCapacity(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, capacity(v))
}

// getStats godoc
//
//	@Summary		Resource calendar statistics
//	@Description	Allocated, used and free room now, per agent (allocated = the teams placed there by the reservations active now, used = what the running objects request) and per event (allocated = the reservation, used = its running objects), with the guaranteed test pool, what test laboratories hold, the pending change requests and the open alarms. Requires infrastructure.read.
//	@Tags			infrastructure
//	@Produce		json
//	@Success		200	{object}	response.Response{data=statsDTO}
//	@Router			/infrastructure/calendar/stats [get]
func (h *Handler) getStats(ctx *gin.Context) {
	v, err := h.useCase.GetResourceCalendarStats(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, stats(v))
}

// getSettings godoc
//
//	@Summary		Resource calendar settings
//	@Description	The guaranteed minimum pool for test laboratories. Requires infrastructure.read.
//	@Tags			infrastructure
//	@Produce		json
//	@Success		200	{object}	response.Response{data=settingsDTO}
//	@Router			/infrastructure/calendar/settings [get]
func (h *Handler) getSettings(ctx *gin.Context) {
	s, err := h.useCase.GetResourceCalendarSettings(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, settingsDTO{TestPool: amount(s.TestPool), UpdatedAt: s.UpdatedAt})
}

// setSettings godoc
//
//	@Summary		Set the guaranteed test pool
//	@Description	The minimum room test laboratories always have (always on, never reserved by events). A pool that the reservations of the next 30 days no longer leave room for is refused with 409 unless AllowConflicts is set; the conflicts are returned. Requires infrastructure.write.
//	@Tags			infrastructure
//	@Accept			json
//	@Produce		json
//	@Param			body	body		setSettingsRequest	true	"pool"
//	@Success		200		{object}	response.Response{data=setSettingsResponse}
//	@Router			/infrastructure/calendar/settings [put]
func (h *Handler) setSettings(ctx *gin.Context) {
	var req setSettingsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	s, cs, err := h.useCase.SetResourceTestPool(ctx, *req.TestPool.model(), req.AllowConflicts)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, setSettingsResponse{Settings: settingsDTO{TestPool: amount(s.TestPool), UpdatedAt: s.UpdatedAt}, Conflicts: conflicts(cs)})
}

// recheck godoc
//
//	@Summary		Run the readiness check now
//	@Description	Completes the teams that had no agent when capacity appeared (it only adds, nothing placed is moved), raises, escalates and resolves the readiness alarms. The same check runs periodically. Requires infrastructure.write.
//	@Tags			infrastructure
//	@Produce		json
//	@Success		200	{object}	response.Response
//	@Router			/infrastructure/calendar/recheck [post]
func (h *Handler) recheck(ctx *gin.Context) {
	if err := h.useCase.ReconcileResourceCalendar(ctx); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// listAlarms godoc
//
//	@Summary		Readiness alarms
//	@Description	Reservations that cannot be served as promised: not_placed (a team fits no agent), agent_lost (an agent they are placed on is gone, disabled, below the requirements or without capacity), agent_shrunk (its capacity fell below what is placed on it, a maintenance window of the agent included), not_connected (the connected capacity at the deploy lead is below the reservation; escalates at 24 h, 2 h and the lead). Open alarms first. Super admins are also notified through the notification system and the error journal (kind lab_readiness). Requires infrastructure.read.
//	@Tags			infrastructure
//	@Produce		json
//	@Param			open	query		string	false	"1 lists only open alarms"
//	@Success		200		{object}	response.Response{data=[]alarmDTO}
//	@Router			/infrastructure/calendar/alarms [get]
func (h *Handler) listAlarms(ctx *gin.Context) {
	items, err := h.useCase.ListResourceAlarms(ctx, ctx.Query("open") == "1")
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]alarmDTO, 0, len(items))
	for _, a := range items {
		out = append(out, alarm(a))
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, out)
}

// acknowledgeAlarm godoc
//
//	@Summary		Acknowledge a readiness alarm
//	@Description	Records that an admin saw the alarm and closes its inbox request; the alarm stays open while its cause lasts. Requires infrastructure.write.
//	@Tags			infrastructure
//	@Produce		json
//	@Param			alarmID	path		string	true	"alarm ID"
//	@Success		200		{object}	response.Response{data=alarmDTO}
//	@Router			/infrastructure/calendar/alarms/{alarmID}/acknowledge [post]
func (h *Handler) acknowledgeAlarm(ctx *gin.Context) {
	id, ok := uuidParam(ctx, "alarmID")
	if !ok {
		return
	}
	by, ok := caller(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "resource_alarm:"+id.String())
	v, err := h.useCase.AcknowledgeResourceAlarm(ctx, id, by)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, alarm(v))
}

// listChangeRequests godoc
//
//	@Summary		Resource change requests
//	@Description	The organizers' requests to change an event's reservation (size, window, estimate for future dynamic tasks) with their reason, newest first, with what the reservation holds now. Requires infrastructure.read.
//	@Tags			infrastructure
//	@Produce		json
//	@Param			status	query		string	false	"pending, approved or rejected"
//	@Param			eventID	query		string	false	"only this event"
//	@Success		200		{object}	response.Response{data=[]changeRequestDTO}
//	@Router			/infrastructure/calendar/change-requests [get]
func (h *Handler) listChangeRequests(ctx *gin.Context) {
	var status *calModel.ChangeStatus
	if s := ctx.Query("status"); s != "" {
		v := calModel.ChangeStatus(s)
		if v != calModel.ChangePending && v != calModel.ChangeApproved && v != calModel.ChangeRejected {
			response.AbortWithBadRequest(ctx)
			return
		}
		status = &v
	}
	var eventID *uuid.UUID
	if s := ctx.Query("eventID"); s != "" {
		id, err := uuid.FromString(s)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		eventID = &id
	}
	items, err := h.useCase.ListResourceChangeRequests(ctx, status, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]changeRequestDTO, 0, len(items))
	for _, c := range items {
		out = append(out, changeRequest(c))
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, out)
}

// decideChangeRequest godoc
//
//	@Summary		Approve or reject a resource change request
//	@Description	Approving extends the existing reservation (size, estimate, window; the tail gap still applies on top of a later end) and places it again keeping every team that still fits. If it no longer fits by packing it is refused with 409 unless AllowConflicts is set (the reservation is then not covered and an alarm is raised). Nothing is moved automatically. Requires infrastructure.write.
//	@Tags			infrastructure
//	@Accept			json
//	@Produce		json
//	@Param			requestID	path		string			true	"change request ID"
//	@Param			body		body		decideRequest	true	"decision"
//	@Success		200			{object}	response.Response{data=changeRequestDTO}
//	@Router			/infrastructure/calendar/change-requests/{requestID}/decision [post]
func (h *Handler) decideChangeRequest(ctx *gin.Context) {
	id, ok := uuidParam(ctx, "requestID")
	if !ok {
		return
	}
	var req decideRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	by, ok := caller(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "resource_change:"+id.String())
	v, err := h.useCase.DecideResourceChangeRequest(ctx, id, req.Approve, req.Note, by, req.AllowConflicts)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, changeRequest(v))
}

// getEventReservation godoc
//
//	@Summary		The reservation of an event
//	@Description	The event's reservation: size, window, where it is placed, whether it is covered, its conflicts and open alarms. 404 when the event has none. Requires infrastructure.read.
//	@Tags			infrastructure
//	@Produce		json
//	@Param			eventID	path		string	true	"event ID"
//	@Success		200		{object}	response.Response{data=reservationResultDTO}
//	@Router			/infrastructure/calendar/events/{eventID}/reservation [get]
func (h *Handler) getEventReservation(ctx *gin.Context) {
	eventID, ok := uuidParam(ctx, "eventID")
	if !ok {
		return
	}
	v, err := h.useCase.GetEventResourceReservation(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, reservationResult(v))
}

// setEventReservation godoc
//
//	@Summary		Set the reservation of an event
//	@Description	Creates or updates the reservation (platform admin only). Size = the resource plan of the event per team x teams + the buffer (default 15%) + the organizer's estimate for future dynamic tasks; the window runs from the stand deploy lead (plus a readiness margin) to the event end + the tail gap (default and minimum 1 h, the admin may set more). The reservation is placed over the agents by packing: a team stays whole on one agent, the fewest agents are used, then agent priority, and elevated tasks only go to agents whose maxima fit. A changed reservation keeps every team that still fits where it is. One that does not fit is refused with 409 (the context names the first conflicting slot) unless AllowConflicts is set; it is then kept, marked not covered, and an alarm is raised. DryRun reports the placement and conflicts without saving. Requires infrastructure.write.
//	@Tags			infrastructure
//	@Accept			json
//	@Produce		json
//	@Param			eventID	path		string					true	"event ID"
//	@Param			body	body		setReservationRequest	true	"reservation"
//	@Success		200		{object}	response.Response{data=reservationResultDTO}
//	@Router			/infrastructure/calendar/events/{eventID}/reservation [put]
func (h *Handler) setEventReservation(ctx *gin.Context) {
	eventID, ok := uuidParam(ctx, "eventID")
	if !ok {
		return
	}
	var req setReservationRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	by, ok := caller(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "event:"+eventID.String())
	v, err := h.useCase.SetEventResourceReservation(ctx, eventID, req.model(), by)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, reservationResult(v))
}

// deleteEventReservation godoc
//
//	@Summary		Cancel the reservation of an event
//	@Description	The room is free at once and the event's alarms close. Requires infrastructure.write.
//	@Tags			infrastructure
//	@Produce		json
//	@Param			eventID	path		string	true	"event ID"
//	@Success		200		{object}	response.Response
//	@Router			/infrastructure/calendar/events/{eventID}/reservation [delete]
func (h *Handler) deleteEventReservation(ctx *gin.Context) {
	eventID, ok := uuidParam(ctx, "eventID")
	if !ok {
		return
	}
	by, ok := caller(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "event:"+eventID.String())
	if err := h.useCase.DeleteEventResourceReservation(ctx, eventID, by); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// replan godoc
//
//	@Summary		Place a reservation again
//	@Description	The admin's manual answer to an alarm (an agent is back or a new one was added): the reservation is placed again over the agents that are used, keeping every team that still fits where it is. Refused with 409 when it still does not fit, unless AllowConflicts is set. Requires infrastructure.write.
//	@Tags			infrastructure
//	@Accept			json
//	@Produce		json
//	@Param			reservationID	path		string			true	"reservation ID"
//	@Param			body			body		replanRequest	false	"options"
//	@Success		200				{object}	response.Response{data=reservationResultDTO}
//	@Router			/infrastructure/calendar/reservations/{reservationID}/replan [post]
func (h *Handler) replan(ctx *gin.Context) {
	id, ok := uuidParam(ctx, "reservationID")
	if !ok {
		return
	}
	var req replanRequest
	if ctx.Request.ContentLength != 0 {
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
	}
	audit.SetTarget(ctx, "resource_reservation:"+id.String())
	v, err := h.useCase.ReplanResourceReservation(ctx, id, req.AllowConflicts)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, reservationResult(v))
}

// getEventResources godoc
//
//	@Summary		Resources of the event (organizer)
//	@Description	What the organizer sees: the allocated size of the event's reservation, what its running objects use, what is free, the window, the buffer and the estimate for future dynamic tasks, whether the reservation is covered, and the change requests with their status. Never names a laboratory. Reserved is false when the platform admin set no reservation for the event. A new task of a running event deploys only if the reservation holds it for all teams; otherwise it is refused (code 72508, "not enough reserved resources, request an extension").
//	@Tags			events
//	@Produce		json
//	@Param			id	path		string	true	"event ID"
//	@Success		200	{object}	response.Response{data=eventResourcesDTO}
//	@Router			/events/{id}/manage/resources [get]
func (h *Handler) getEventResources(ctx *gin.Context) {
	eventID, ok := uuidParam(ctx, "id")
	if !ok {
		return
	}
	v, err := h.useCase.GetEventResources(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, eventResources(v))
}

// requestChange godoc
//
//	@Summary		Ask for a change of the event's resources (organizer)
//	@Description	Sends the platform admin a request to change the event's reservation: the size, the window (the tail gap still applies on top of a later end) and/or the estimate for future dynamic tasks, with a reason. One request waits at a time (409 while one is pending); extending the existing reservation is the way. 404 when the event has no reservation. The admin approves or rejects it; the organizer is told the outcome.
//	@Tags			events
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"event ID"
//	@Param			body	body		requestChangeRequest	true	"change"
//	@Success		200		{object}	response.Response{data=organizerChangeDTO}
//	@Router			/events/{id}/manage/resources/change-requests [post]
func (h *Handler) requestChange(ctx *gin.Context) {
	eventID, ok := uuidParam(ctx, "id")
	if !ok {
		return
	}
	var req requestChangeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	by, ok := caller(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.RequestResourceChange(ctx, eventID, by, calUseCase.ChangeInput{
		Size: req.Size.model(), Dynamic: req.Dynamic.model(), WindowStart: req.WindowStart, WindowEnd: req.WindowEnd, Reason: req.Reason,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, organizerChange(v))
}

func queryInt64(ctx *gin.Context, name string) int64 {
	v, _ := strconv.ParseInt(ctx.Query(name), 10, 64)
	return max(v, 0)
}

// testLabRoom godoc
//
//	@Summary		Does a test laboratory fit now
//	@Description	For a catalog author: whether a test laboratory of this size starts now and how (inside the author's booked window, the guaranteed pool, or room no event has reserved), or, when there is none, the start of the nearest free window the author can book. Nothing is held. Size is the laboratory's total (the catalog shows it per exercise).
//	@Tags			exercises
//	@Produce		json
//	@Param			cpu				query		int	true	"laboratory CPU, millicores"
//	@Param			memory			query		int	true	"laboratory memory, bytes"
//	@Param			deviceCpu		query		int	false	"largest device CPU, millicores"
//	@Param			deviceMemory	query		int	false	"largest device memory, bytes"
//	@Param			leaseMinutes	query		int	false	"how long it will run (default 120)"
//	@Success		200				{object}	response.Response{data=testLabRoomDTO}
//	@Router			/exercises/test-labs/room [get]
func (h *Handler) testLabRoom(ctx *gin.Context) {
	owner, ok := caller(ctx)
	if !ok {
		return
	}
	size := calUseCase.Amount{CPUMillicores: queryInt64(ctx, "cpu"), MemoryBytes: queryInt64(ctx, "memory")}
	if size == (calUseCase.Amount{}) {
		response.AbortWithBadRequest(ctx)
		return
	}
	device := calUseCase.Amount{CPUMillicores: queryInt64(ctx, "deviceCpu"), MemoryBytes: queryInt64(ctx, "deviceMemory")}
	room, err := h.useCase.CheckTestLabRoom(ctx, owner, size, device, time.Duration(queryInt64(ctx, "leaseMinutes"))*time.Minute)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, testLabRoomDTO{Available: room.Available, Via: room.Via, NearestFrom: room.NearestFrom})
}

// listBookings godoc
//
//	@Summary		My test laboratory bookings
//	@Description	The author's booked windows that have not ended.
//	@Tags			exercises
//	@Produce		json
//	@Success		200	{object}	response.Response{data=[]bookingDTO}
//	@Router			/exercises/test-labs/bookings [get]
func (h *Handler) listBookings(ctx *gin.Context) {
	owner, ok := caller(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListTestLabBookings(ctx, owner)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]bookingDTO, 0, len(items))
	for _, b := range items {
		out = append(out, booking(b))
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, out)
}

// book godoc
//
//	@Summary		Book a window for a test laboratory
//	@Description	Reserves room in the calendar for the author's test laboratory (for example 2 hours from HH:MM, 15 minutes to 8 hours, at most 14 days ahead, 3 bookings at once). The laboratory started inside the window uses the booking. When the window does not fit, 409 with the nearest window that does ("nearest_from" in the error context).
//	@Tags			exercises
//	@Accept			json
//	@Produce		json
//	@Param			body	body		bookRequest	true	"booking"
//	@Success		200		{object}	response.Response{data=bookingDTO}
//	@Router			/exercises/test-labs/bookings [post]
func (h *Handler) book(ctx *gin.Context) {
	owner, ok := caller(ctx)
	if !ok {
		return
	}
	var req bookRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.BookTestLab(ctx, owner, req.Start, time.Duration(req.DurationMinutes)*time.Minute, *req.Size.model(), *req.LargestDevice.model())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, booking(v))
}

// cancelBooking godoc
//
//	@Summary		Cancel a test laboratory booking
//	@Description	The author's own booking; its room is free at once.
//	@Tags			exercises
//	@Produce		json
//	@Param			bookingID	path		string	true	"booking ID"
//	@Success		200			{object}	response.Response
//	@Router			/exercises/test-labs/bookings/{bookingID} [delete]
func (h *Handler) cancelBooking(ctx *gin.Context) {
	owner, ok := caller(ctx)
	if !ok {
		return
	}
	id, ok := uuidParam(ctx, "bookingID")
	if !ok {
		return
	}
	if err := h.useCase.CancelTestLabBooking(ctx, owner, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
