package event

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// upcomingCacheControl lets browsers and shared caches reuse the anonymous
// landing-page answer briefly; it carries no per-user data.
const upcomingCacheControl = "public, max-age=60"

// upcomingEventResponse is the public card of the event advertised on the
// platform landing page. URL is the event's own site, https://<Tag>.<domain>.
type upcomingEventResponse struct {
	Tag          string     `json:"Tag"`
	Name         string     `json:"Name"`
	URL          string     `json:"URL"`
	StartAt      time.Time  `json:"StartAt"`
	FinishAt     *time.Time `json:"FinishAt"`
	Registration string     `json:"Registration" enums:"open,approval,close"`
	LogoURL      *string    `json:"LogoURL"`
}

// upcoming godoc
// @Summary      Get the nearest public event for the platform landing page
// @Description  Public (no auth). Currently always returns data: null — events have no public visibility yet. Once they do: a running public event wins (earliest start among started), otherwise the published public event with the nearest future start. Cacheable for 60 seconds.
// @Tags         events
// @Produce      json
// @Success      200  {object}  response.Response{data=upcomingEventResponse}
// @Router       /events/upcoming [get]
func (h *Handler) upcoming(ctx *gin.Context) {
	// TODO(public-events): return the nearest public event once events get a
	// public visibility flag (all events are private by default). Intended rule:
	// among PUBLIC events whose lifecycle status at the request time is Started
	// or Published (never NotPublished, Finished or Withdrawn), prefer a Started
	// one (earliest StartAt), otherwise the Published one with the nearest
	// future StartAt; data stays null when none qualifies. Fill
	// upcomingEventResponse: Tag, Name, URL (https://<Tag>.<platform domain>),
	// StartAt, FinishAt (effective finish, nullable), Registration from the
	// event config ("open" | "approval" | "close"; "close" when no config) and
	// LogoURL (nullable, once event branding stores a logo).
	var data *upcomingEventResponse
	ctx.Header("Cache-Control", upcomingCacheControl)
	response.AbortWithData(ctx, data)
}
