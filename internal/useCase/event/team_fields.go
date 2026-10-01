package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnswerFileRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
)

// Team fields use the same document and answer validation as participant
// fields, but their values belong to the competitive team.
func (u *EventUseCase) GetTeamFields(ctx context.Context, eventID uuid.UUID) (ParticipantFormView, error) {
	form, err := u.teams.GetFieldConfig(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return ParticipantFormView{}, eventTeamModel.ErrEventTeamNotFound.Err()
		}
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	answered, err := u.teams.Count(ctx, eventID)
	if err != nil {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count teams").Err()
	}
	return ParticipantFormView{Version: form.Version, Enabled: form.Enabled, Required: form.Required, Document: form.Document,
		RequireExisting: form.RequireExisting, BlockSubmissions: form.BlockSubmissions, Answered: answered}, nil
}

// validateTeamFieldAnswers checks team answers against the team fields and
// binds their files: actor is who saves (uuid.Nil for event staff), owner the
// team (uuid.Nil for a team not created yet). The returned answers are the
// ones to save; call attach with the team ID after saving them. partial
// (an organizer's CSV import) checks the value types only: required fields
// may stay empty and files are refused.
func validateTeamFieldAnswers(ctx context.Context, repo IRepository, eventID, actor, owner uuid.UUID, answers map[string]any, partial bool) (boundAnswers, error) {
	return validateTeamFieldAnswersKeeping(ctx, repo, eventID, actor, owner, answers, partial, nil)
}

// validateTeamFieldAnswersKeeping is validateTeamFieldAnswers for a team that
// existed before a field became required: with stored (its saved answers) and
// no «required from every team» policy, a required field it had left empty
// may stay empty. A nil stored keeps every required field demanded.
func validateTeamFieldAnswersKeeping(ctx context.Context, repo IRepository, eventID, actor, owner uuid.UUID, answers map[string]any, partial bool, stored map[string]any) (boundAnswers, error) {
	unchanged := boundAnswers{values: answers}
	form, err := eventTeamRepo.New(repo).GetFieldConfig(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			if len(answers) == 0 {
				return unchanged, nil
			}
			return boundAnswers{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage("Team fields are not configured").Err()
		}
		return boundAnswers{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	if !form.Enabled {
		if len(answers) == 0 {
			return unchanged, nil
		}
		return boundAnswers{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage("Team fields are disabled").Err()
	}
	if !form.Required && len(answers) == 0 {
		return unchanged, nil
	}
	if actor != uuid.Nil {
		// Captains and members never write staff-only fields.
		answers = form.WithoutStaffAnswers(answers)
	}
	allowed := make(map[string]struct{})
	for _, block := range form.Document.Blocks {
		if block.Type == "field" {
			allowed[block.Key] = struct{}{}
		}
	}
	for key := range answers {
		if _, ok := allowed[key]; !ok {
			return boundAnswers{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage("Unknown team field: " + key).Err()
		}
	}
	if partial {
		if err := form.ValidatePartialAnswers(answers); err != nil {
			return boundAnswers{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage(err.Error()).Err()
		}
		unchanged.missing = int32(len(missingRequired(form, answers)))
		return unchanged, nil
	}
	bound, err := bindAnswerFiles(ctx, eventAnswerFileRepo.New(repo), eventID, eventFormModel.AnswerScopeTeam, actor, owner, form, answers)
	if err != nil {
		return boundAnswers{}, err
	}
	validate := form.ValidateAnswers
	if stored != nil && !form.RequireExisting {
		validate = func(values map[string]any) error { return form.ValidateAnswersKeepingGaps(values, stored) }
	}
	if err := validate(bound.values); err != nil {
		return boundAnswers{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage(err.Error()).Err()
	}
	bound.missing = int32(len(missingRequired(form, bound.values)))
	return bound, nil
}

func (u *EventUseCase) ConfigureTeamFields(ctx context.Context, eventID uuid.UUID, in ConfigureParticipantFormInput) (ParticipantFormView, error) {
	form := eventFormModel.Form{Enabled: in.Enabled, Required: in.Required, Document: in.Document}
	if err := form.Validate(); err != nil {
		return ParticipantFormView{}, eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage(err.Error()).Err()
	}
	var previous eventFormModel.Form
	if current, err := u.teams.GetFieldConfig(ctx, eventID); err == nil {
		previous = current
	} else if !repositoryTools.IsObjectNotFoundError(err) && err != pgx.ErrNoRows {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
	}
	form.RequireExisting, form.BlockSubmissions = inheritPolicy(previous, in.RequireExisting, in.BlockSubmissions)
	created, err := u.teams.PutFieldConfig(ctx, eventID, form, time.Now().UTC())
	if err != nil {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save team fields").Err()
	}
	if err = recountTeamFields(ctx, u.teams, eventID, created); err != nil {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to recount team fields").Err()
	}
	answered, err := u.teams.Count(ctx, eventID)
	if err != nil {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count teams").Err()
	}
	return ParticipantFormView{Version: created.Version, Enabled: created.Enabled, Required: created.Required, Document: created.Document,
		RequireExisting: created.RequireExisting, BlockSubmissions: created.BlockSubmissions, Answered: answered}, nil
}
