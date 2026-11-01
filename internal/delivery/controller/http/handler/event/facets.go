package event

import (
	"slices"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"errors"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type (
	facetResponse struct {
		// Counts maps a value of the field to the number of events with it under the other filters; values with no
		// events are absent.
		Counts map[string]int64 `json:"Counts"`
	}

	organizerResponse struct {
		UserID uuid.UUID `json:"UserID"`
		Name   string    `json:"Name"`
		Email  string    `json:"Email"`
	}
)

// facets godoc
// @Summary  Facet counts of the events table
// @Description How many events have each value of field (status, infrastructure or organizer) under the same filters as GET /events (search, status, startFrom, startTo, organizer, infrastructure, needsReservation) except the field's own.
// @Tags     events
// @Produce  json
// @Param    field  query  string  true  "status | infrastructure | organizer"
// @Success  200  {object}  response.Response{data=facetResponse}
// @Router   /events/facets [get]
func (h *Handler) facets(ctx *gin.Context) {
	field := ctx.Query("field")
	if !slices.Contains(eventUseCase.EventFacetFields(), field) {
		response.AbortWithBadRequest(
			ctx,
			errors.New("field must be status, infrastructure or organizer"),
		)
		return
	}
	filter := eventUseCase.ListEventsFilter{
		Search: ctx.Query("search"),
		Status: ctx.Query("status"),
	}
	if err := parseEventListFilters(ctx, &filter); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	counts, err := h.useCase.EventFacets(ctx.Request.Context(), field, filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, facetResponse{Counts: counts})
}

// organizers godoc
// @Summary  Everyone who manages at least one event
// @Description Feeds the organizer filter of the events table.
// @Tags     events
// @Produce  json
// @Success  200  {object}  response.Response{data=[]organizerResponse}
// @Router   /events/organizers [get]
func (h *Handler) organizers(ctx *gin.Context) {
	list, err := h.useCase.ListEventOrganizers(ctx.Request.Context())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]organizerResponse, 0, len(list))
	for _, o := range list {
		out = append(out, organizerResponse{UserID: o.UserID, Name: o.Name, Email: o.Email})
	}
	response.AbortWithData(ctx, out)
}
