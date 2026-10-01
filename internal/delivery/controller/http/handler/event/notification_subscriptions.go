package event

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// notificationSubscriptionResponse is the effective subscription of an Event.
// Source is "platform" when inherited from the platform default and "event"
// when the Event overrides it.
type notificationSubscriptionResponse struct {
	SignalType string          `json:"SignalType"`
	Channel    string          `json:"Channel"`
	Enabled    bool            `json:"Enabled"`
	Audience   json.RawMessage `json:"Audience" swaggertype:"object"`
	// Config holds the per-signal options: days_before_start (1-30) for the
	// start reminder, {} for the others.
	Config json.RawMessage `json:"Config" swaggertype:"object"`
	Source string          `json:"Source" enums:"platform,event"`
	// Required: invitation email, always sent; PUT is rejected (72103).
	Required bool `json:"Required"`
}

type upsertNotificationSubscriptionRequest struct {
	SignalType string          `json:"SignalType"`
	Channel    string          `json:"Channel"`
	Enabled    bool            `json:"Enabled"`
	Audience   json.RawMessage `json:"Audience" swaggertype:"object"`
	// Config is optional; omitted keeps the stored options.
	Config json.RawMessage `json:"Config,omitempty" swaggertype:"object"`
}

func toNotificationSubscriptionResponse(v eventUseCase.NotificationSubscriptionView) notificationSubscriptionResponse {
	return notificationSubscriptionResponse{SignalType: v.SignalType, Channel: v.Channel, Enabled: v.Enabled, Audience: v.Audience, Config: v.Config, Source: v.Source, Required: v.Required}
}

// listNotificationSubscriptions godoc
// @Summary List effective event notification subscriptions
// @Description Every Event-scoped (signal, channel) pair: the Event override when present (Source=event), otherwise the inherited platform default (Source=platform).
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]notificationSubscriptionResponse}
// @Router /events/{id}/manage/notification-subscriptions [get]
func (h *Handler) listNotificationSubscriptions(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListNotificationSubscriptions(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]notificationSubscriptionResponse, 0, len(items))
	for _, item := range items {
		out = append(out, toNotificationSubscriptionResponse(item))
	}
	response.AbortWithData(ctx, out)
}

// upsertNotificationSubscription godoc
// @Summary Create or update an event notification subscription
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body upsertNotificationSubscriptionRequest true "subscription"
// @Success 200 {object} response.Response{data=notificationSubscriptionResponse}
// @Router /events/{id}/manage/notification-subscriptions [put]
func (h *Handler) upsertNotificationSubscription(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req upsertNotificationSubscriptionRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	item, err := h.useCase.UpsertNotificationSubscription(ctx, eventID, eventUseCase.UpsertNotificationSubscriptionInput(req))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toNotificationSubscriptionResponse(item))
}

// resetNotificationSubscription godoc
// @Summary Reset an event notification subscription to the platform default
// @Description Removes the Event override of one (signal, channel) pair so it inherits the platform default again. Idempotent.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param signalType path string true "signal type"
// @Param channel path string true "channel (email|in_app)"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/notification-subscriptions/{signalType}/{channel} [delete]
func (h *Handler) resetNotificationSubscription(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.ResetNotificationSubscription(ctx, eventID, ctx.Param("signalType"), ctx.Param("channel")); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
