package event

import (
	"context"
	"fmt"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// EventPreviewValues are the variable values the template previews of the event use (eventPreviewValues).
func (u *EventUseCase) EventPreviewValues(ctx context.Context, eventID uuid.UUID) (map[string]string, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return eventPreviewValues(e, u.eventDomain), nil
}

// eventPreviewValues are the variable values of this event's template previews (email and in-app): the event's
// own name, tag, site links in the format dispatch uses and its times. What only a real recipient has (a name, an
// address, a team) is a clear placeholder; the invitation link is the event's own one without a setup token.
func eventPreviewValues(e eventModel.Event, eventDomain string) map[string]string {
	site := fmt.Sprintf("https://%s.%s", e.Tag, eventDomain)
	values := map[string]string{
		"scope_event_id":  e.ID.String(),
		"event_tag":       e.Tag,
		"event_name":      e.Name,
		"event_url":       site + "/",
		"invite_url":      site + "/invite",
		"team_url":        site + "/participation?tab=team",
		"start_at":        kyivTime(e.Lifecycle.StartAt),
		"user_first_name": "Імʼя",
		"user_last_name":  "Прізвище",
		"user_name":       "Імʼя учасника",
		"user_email":      "participant@" + e.Tag + "." + eventDomain,
		"team_name":       "Назва команди",
	}
	if e.Lifecycle.FinishAt != nil {
		values["finish_at"] = kyivTime(*e.Lifecycle.FinishAt)
	}
	return values
}
