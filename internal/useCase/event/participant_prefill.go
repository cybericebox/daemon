package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
)

// Organizers may prefill participant form answers when they invite people
// (the CSV import). The answers are saved as the person's own registration
// answers for the latest form version; required fields may stay empty, the
// person completes them before accepting (requireParticipantForm), and a
// non-editable field with a value stays locked (participantModel.LockFilledAnswers).

// prefillForm returns the latest participant form when it can take prefilled
// answers, nil when there is none or it is disabled.
func prefillForm(ctx context.Context, forms *eventFormRepo.Repository, eventID uuid.UUID) (*eventFormRepo.Version, error) {
	version, err := forms.Latest(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form").Err()
	}
	if !version.Form.Enabled {
		return nil, nil
	}
	return &version, nil
}

// savePrefilledAnswers merges values over the person's stored answers and
// saves them for the given form version.
func savePrefilledAnswers(ctx context.Context, forms *eventFormRepo.Repository, eventID, userID uuid.UUID, version eventFormRepo.Version, values map[string]any, now time.Time) error {
	stored, err := forms.LatestRegistrationAnswers(ctx, eventID, []uuid.UUID{userID})
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant answers").Err()
	}
	merged := make(map[string]any, len(stored[userID])+len(values))
	for key, value := range stored[userID] {
		merged[key] = value
	}
	for key, value := range values {
		merged[key] = value
	}
	if _, err = forms.SaveAnswer(ctx, eventFormRepo.Answer{EventID: eventID, UserID: userID, FormVersionID: version.ID, Values: merged, SubmittedAt: now}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save prefilled participant answers").Err()
	}
	return setParticipantFields(ctx, forms, eventID, userID, version.Form, merged)
}

// validatePrefill checks an entry's values against the form; ok=false means
// the entry cannot carry them (no usable form, or a value of the wrong type).
func validatePrefill(version *eventFormRepo.Version, values map[string]any) bool {
	if len(values) == 0 {
		return true
	}
	return version != nil && version.Form.ValidatePartialAnswers(values) == nil
}

// GetOwnParticipantFormAnswers returns the caller's stored registration
// answers (prefilled by organizers or submitted earlier), for the invite and
// join forms to show. Staff-only fields are never returned; an empty map when
// there is nothing.
func (u *EventUseCase) GetOwnParticipantFormAnswers(ctx context.Context, eventID, userID uuid.UUID) (map[string]any, error) {
	latest, err := u.forms.LatestRegistrationAnswers(ctx, eventID, []uuid.UUID{userID})
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant answers").Err()
	}
	out := map[string]any{}
	if len(latest[userID]) == 0 {
		return out, nil
	}
	hidden := map[string]struct{}{}
	if form, formErr := u.forms.Latest(ctx, eventID); formErr == nil {
		for _, block := range form.Form.Document.Blocks {
			if block.Type == eventContentModel.BlockField && block.StaffOnly {
				hidden[block.Key] = struct{}{}
			}
		}
	}
	for key, value := range latest[userID] {
		if _, staff := hidden[key]; !staff {
			out[key] = value
		}
	}
	return out, nil
}

// participantFormComplete says whether the stored answer satisfies the form.
func participantFormComplete(form eventFormModel.Form, values map[string]any) bool {
	return form.ValidateAnswers(values) == nil
}
