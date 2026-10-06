package event

import (
	"context"

	"github.com/gofrs/uuid"

	eventFormRepo "github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// requireEventCapability checks only a delivery that explicitly selected the
// requested capability. It deliberately does not infer a global onboarding
// block from the existence of any form or answer.
func requireEventCapability(ctx context.Context, forms *eventFormRepo.Repository, eventID, userID uuid.UUID, capability eventFormModel.Capability) error {
	blocked, err := forms.HasIncompleteRequiredDelivery(ctx, eventID, userID, capability)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check required event form").Err()
	}
	if blocked {
		return participantModel.ErrEventFormRequired.Err()
	}
	return nil
}
