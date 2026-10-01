package event

import (
	"context"
	"reflect"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// Staff-only fields are filled by organizers in the participants and teams
// tables. Their values live next to the other answers (the participant's
// newest answer row, the team's extra fields) but no participant-facing view
// ever carries them; every edit is audited (who, when, which fields).

const (
	staffScopeParticipant = "participant"
	staffScopeTeam        = "team"
)

// StaffFieldsView is the staff-only values of one participant or team with
// the last edit.
type StaffFieldsView struct {
	Values map[string]any
	// Change is nil when the fields were never edited.
	Change *StaffChangeView
}

type StaffChangeView struct {
	Keys      []string
	ActorID   *uuid.UUID
	ActorName string
	At        time.Time
}

// GetParticipantStaffFields returns the participant's staff-only values.
func (u *EventUseCase) GetParticipantStaffFields(ctx context.Context, eventID, userID uuid.UUID) (StaffFieldsView, error) {
	form, err := u.registrationForm(ctx, eventID)
	if err != nil {
		return StaffFieldsView{}, err
	}
	if _, err = u.participants.Get(ctx, eventID, userID); err != nil {
		return StaffFieldsView{}, participantLookupError(err)
	}
	row, err := u.forms.LatestAnswerRow(ctx, eventID, userID)
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) && err != pgx.ErrNoRows {
		return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant answers").Err()
	}
	return u.staffFieldsView(ctx, u.forms, eventID, staffScopeParticipant, userID, form, row.Values)
}

// UpdateParticipantStaffFields saves the participant's staff-only values: the
// given keys change (an empty value clears one), the others stay.
func (u *EventUseCase) UpdateParticipantStaffFields(ctx context.Context, eventID, userID, actorID uuid.UUID, values map[string]any) (StaffFieldsView, error) {
	if u.uow == nil {
		return StaffFieldsView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return StaffFieldsView{}, err
	}
	defer unit.Restore()
	forms := eventFormRepo.New(txRepo)
	latest, err := forms.Latest(txCtx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return StaffFieldsView{}, eventModel.ErrParticipantFormNotFound.Err()
		}
		return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form").Err()
	}
	if _, err = participantRepo.New(txRepo).Get(txCtx, eventID, userID); err != nil {
		return StaffFieldsView{}, participantLookupError(err)
	}
	if err = latest.Form.ValidateStaffAnswers(values); err != nil {
		return StaffFieldsView{}, participantModel.ErrParticipantStaffFieldsInvalid.WithMessage(err.Error()).Err()
	}
	row, err := forms.LatestAnswerRow(txCtx, eventID, userID)
	found := err == nil
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) && err != pgx.ErrNoRows {
		return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant answers").Err()
	}
	merged, changed := mergeStaffValues(row.Values, values)
	if len(changed) > 0 {
		now := time.Now().UTC()
		if found {
			if _, err = forms.UpdateAnswerValues(txCtx, eventID, userID, row.FormVersionID, merged); err != nil {
				return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save staff-only fields").Err()
			}
		} else if _, err = forms.SaveAnswer(txCtx, eventFormRepo.Answer{EventID: eventID, UserID: userID, FormVersionID: latest.ID, Values: merged, SubmittedAt: now}); err != nil {
			return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save staff-only fields").Err()
		}
		if err = forms.RecordStaffChange(txCtx, eventID, staffScopeParticipant, userID, actorID, changed, now); err != nil {
			return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record staff-only fields change").Err()
		}
	}
	view, err := u.staffFieldsView(txCtx, forms, eventID, staffScopeParticipant, userID, latest.Form, merged)
	if err != nil {
		return StaffFieldsView{}, err
	}
	if err = unit.Save(); err != nil {
		return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save staff-only fields").Err()
	}
	return view, nil
}

// GetTeamStaffFields returns the team's staff-only values.
func (u *EventUseCase) GetTeamStaffFields(ctx context.Context, eventID, teamID uuid.UUID) (StaffFieldsView, error) {
	form, err := u.teams.GetFieldConfig(ctx, eventID)
	if err != nil {
		return StaffFieldsView{}, teamFieldsConfigError(err)
	}
	current, err := u.teams.GetExtraFields(ctx, eventID, teamID)
	if err != nil {
		return StaffFieldsView{}, teamLookupError(err)
	}
	return u.staffFieldsView(ctx, u.forms, eventID, staffScopeTeam, teamID, form, current)
}

// UpdateTeamStaffFields saves the team's staff-only values like
// UpdateParticipantStaffFields does for a participant.
func (u *EventUseCase) UpdateTeamStaffFields(ctx context.Context, eventID, teamID, actorID uuid.UUID, values map[string]any) (StaffFieldsView, error) {
	if u.uow == nil {
		return StaffFieldsView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return StaffFieldsView{}, err
	}
	defer unit.Restore()
	teams := eventTeamRepo.New(txRepo)
	form, err := teams.GetFieldConfig(txCtx, eventID)
	if err != nil {
		return StaffFieldsView{}, teamFieldsConfigError(err)
	}
	if err = form.ValidateStaffAnswers(values); err != nil {
		return StaffFieldsView{}, eventTeamModel.ErrEventTeamStaffFieldsInvalid.WithMessage(err.Error()).Err()
	}
	current, err := teams.GetExtraFields(txCtx, eventID, teamID)
	if err != nil {
		return StaffFieldsView{}, teamLookupError(err)
	}
	merged, changed := mergeStaffValues(current, values)
	forms := eventFormRepo.New(txRepo)
	if len(changed) > 0 {
		if _, err = teams.UpdateExtraFields(txCtx, eventID, teamID, merged); err != nil {
			return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save staff-only fields").Err()
		}
		if err = forms.RecordStaffChange(txCtx, eventID, staffScopeTeam, teamID, actorID, changed, time.Now().UTC()); err != nil {
			return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record staff-only fields change").Err()
		}
	}
	view, err := u.staffFieldsView(txCtx, forms, eventID, staffScopeTeam, teamID, form, merged)
	if err != nil {
		return StaffFieldsView{}, err
	}
	if err = unit.Save(); err != nil {
		return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save staff-only fields").Err()
	}
	return view, nil
}

// mergeStaffValues applies the edit on top of the stored answers: an empty
// value clears its key. changed lists the keys whose value actually differs.
func mergeStaffValues(stored, edit map[string]any) (map[string]any, []string) {
	merged := make(map[string]any, len(stored)+len(edit))
	for key, value := range stored {
		merged[key] = value
	}
	changed := []string{}
	for key, value := range edit {
		if emptyAnswer(value) {
			if _, had := merged[key]; had && !emptyAnswer(merged[key]) {
				changed = append(changed, key)
			}
			delete(merged, key)
			continue
		}
		if !reflect.DeepEqual(merged[key], value) {
			changed = append(changed, key)
		}
		merged[key] = value
	}
	sort.Strings(changed)
	return merged, changed
}

func (u *EventUseCase) staffFieldsView(ctx context.Context, forms *eventFormRepo.Repository, eventID uuid.UUID, scope string, subjectID uuid.UUID, form eventFormModel.Form, answers map[string]any) (StaffFieldsView, error) {
	staff := form.StaffKeys()
	values := make(map[string]any, len(staff))
	for key, value := range answers {
		if staff[key] {
			values[key] = value
		}
	}
	view := StaffFieldsView{Values: values}
	change, found, err := forms.LastStaffChange(ctx, eventID, scope, subjectID)
	if err != nil {
		return StaffFieldsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get staff-only fields change").Err()
	}
	if found {
		view.Change = &StaffChangeView{Keys: change.Keys, ActorID: change.ActorID, ActorName: change.ActorName, At: change.At}
	}
	return view, nil
}

func (u *EventUseCase) registrationForm(ctx context.Context, eventID uuid.UUID) (eventFormModel.Form, error) {
	latest, err := u.forms.Latest(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return eventFormModel.Form{}, eventModel.ErrParticipantFormNotFound.Err()
		}
		return eventFormModel.Form{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form").Err()
	}
	return latest.Form, nil
}

func participantLookupError(err error) error {
	if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
		return participantModel.ErrParticipantNotFound.Err()
	}
	return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
}

func teamLookupError(err error) error {
	if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
		return eventTeamModel.ErrEventTeamNotFound.Err()
	}
	return model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
}

func teamFieldsConfigError(err error) error {
	if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
		return eventTeamModel.ErrEventTeamFieldsInvalid.WithMessage("Team fields are not configured").Err()
	}
	return model.ErrPlatform.WithError(err).WithMessage("Failed to get team fields").Err()
}
