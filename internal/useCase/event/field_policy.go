package event

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// A required field added while people already answered can be asked from
// everyone (Form.RequireExisting): participants and teams then carry the
// number of required fields they have not filled (fields_missing). Nobody is
// blocked by it, except from submitting solutions when Form.BlockSubmissions
// is on too. The number is recomputed whenever answers or the form change.

// missingRequired lists the required fields the answers leave empty, or
// nothing when the policy does not ask existing people to fill them (an
// optional form never asks).
func missingRequired(form eventFormModel.Form, values map[string]any) []string {
	if !form.Enabled || !form.Required || !form.RequireExisting {
		return []string{}
	}
	return form.MissingRequired(values)
}

// blocksSubmissions reports whether the missing fields block solution
// submissions under the form's policy.
func blocksSubmissions(form eventFormModel.Form, missing []string) bool {
	return form.Enabled && form.RequireExisting && form.BlockSubmissions && len(missing) > 0
}

// recountParticipantFields recomputes fields_missing of every participant of
// the event against the registration form.
func recountParticipantFields(ctx context.Context, forms *eventFormRepo.Repository, eventID uuid.UUID, form eventFormModel.Form) error {
	rows, err := forms.ListParticipantAnswers(ctx, eventID)
	if err != nil {
		return err
	}
	counts := make(map[uuid.UUID]int32, len(rows))
	for _, row := range rows {
		counts[row.UserID] = int32(len(missingRequired(form, row.Values)))
	}
	return forms.SetFieldsMissing(ctx, eventID, counts)
}

// setParticipantFields stores fields_missing of one participant after their
// answers were saved.
func setParticipantFields(ctx context.Context, forms *eventFormRepo.Repository, eventID, userID uuid.UUID, form eventFormModel.Form, values map[string]any) error {
	if !form.Enabled || !form.Required || !form.RequireExisting {
		return nil // nobody is counted while the policy is off (the recount zeroed it)
	}
	return forms.SetFieldsMissing(ctx, eventID, map[uuid.UUID]int32{userID: int32(len(missingRequired(form, values)))})
}

// recountTeamFields recomputes fields_missing of every team of the event
// against the team fields.
func recountTeamFields(ctx context.Context, teams *eventTeamRepo.Repository, eventID uuid.UUID, form eventFormModel.Form) error {
	rows, err := teams.ListTeamFields(ctx, eventID)
	if err != nil {
		return err
	}
	counts := make(map[uuid.UUID]int32, len(rows))
	for _, row := range rows {
		counts[row.TeamID] = int32(len(missingRequired(form, row.Values)))
	}
	return teams.SetFieldsMissing(ctx, eventID, counts)
}

// setTeamFields stores fields_missing of one team after its fields were saved.
func setTeamFields(ctx context.Context, teams *eventTeamRepo.Repository, eventID, teamID uuid.UUID, form eventFormModel.Form, values map[string]any) error {
	return teams.SetFieldsMissing(ctx, eventID, map[uuid.UUID]int32{teamID: int32(len(missingRequired(form, values)))})
}

// requireFieldsFilled rejects a solution submission while the participant (or
// their team) has unfilled required fields and the form's policy blocks
// submissions: the same form_required rejection as a required form delivery.
func requireFieldsFilled(ctx context.Context, forms *eventFormRepo.Repository, teams *eventTeamRepo.Repository, eventID, userID, teamID uuid.UUID) error {
	blocked, err := participantFieldsBlock(ctx, forms, eventID, userID)
	if err == nil && !blocked {
		blocked, err = teamFieldsBlock(ctx, teams, eventID, teamID)
	}
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check required fields").Err()
	}
	if blocked {
		return participantModel.ErrEventFormRequired.Err()
	}
	return nil
}

// participantFieldsBlock: the number is checked first, so the form is read
// only for someone who really has gaps.
func participantFieldsBlock(ctx context.Context, forms *eventFormRepo.Repository, eventID, userID uuid.UUID) (bool, error) {
	missing, err := forms.FieldsMissing(ctx, eventID, userID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if missing == 0 {
		return false, nil
	}
	latest, err := forms.Latest(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return latest.Form.Enabled && latest.Form.RequireExisting && latest.Form.BlockSubmissions, nil
}

func teamFieldsBlock(ctx context.Context, teams *eventTeamRepo.Repository, eventID, teamID uuid.UUID) (bool, error) {
	missing, err := teams.FieldsMissing(ctx, eventID, teamID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	if missing == 0 {
		return false, nil
	}
	config, err := teams.GetFieldConfig(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return false, nil
		}
		return false, err
	}
	return config.Enabled && config.RequireExisting && config.BlockSubmissions, nil
}

// inheritPolicy resolves the policy of a saved form: an explicit choice wins,
// otherwise the previous version's policy carries over so a later edit does
// not silently drop «required from everyone».
func inheritPolicy(previous eventFormModel.Form, requireExisting, blockSubmissions *bool) (bool, bool) {
	existing, block := previous.RequireExisting, previous.BlockSubmissions
	if requireExisting != nil {
		existing = *requireExisting
	}
	if blockSubmissions != nil {
		block = *blockSubmissions
	}
	return existing, existing && block
}

// saveTeamFields stores a team's field answers together with its
// missing-field number.
func saveTeamFields(ctx context.Context, repo IRepository, eventID, teamID uuid.UUID, bound boundAnswers) error {
	_, err := eventTeamRepo.New(repo).SaveExtraFields(ctx, eventID, teamID, bound.values, bound.missing)
	return err
}

// keepTeamStaffValues puts the stored staff-only values back into the answers
// a moderator saves through the team form.
func keepTeamStaffValues(ctx context.Context, teams *eventTeamRepo.Repository, eventID, teamID uuid.UUID, values map[string]any) (map[string]any, error) {
	form, err := teams.GetFieldConfig(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return values, nil
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	if len(form.StaffKeys()) == 0 {
		return values, nil
	}
	current, err := teams.GetExtraFields(ctx, eventID, teamID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	return form.KeepStaffAnswers(current, values), nil
}
