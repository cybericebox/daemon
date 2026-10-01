package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// GetOwnParticipantAnswers returns the approved caller's latest registration
// answers with the latest participant form and whether they are editable.
func (u *EventUseCase) GetOwnParticipantAnswers(ctx context.Context, eventID, userID uuid.UUID) (OwnParticipantAnswersView, error) {
	state, err := u.ownParticipantAnswers(ctx, eventID, userID)
	if err != nil {
		return OwnParticipantAnswersView{}, err
	}
	return state.view(), nil
}

// UpdateOwnParticipantAnswers lets an approved participant change the
// editable answers until the event's effective finish. The merged answers
// are validated against the latest form and saved as its answer.
func (u *EventUseCase) UpdateOwnParticipantAnswers(ctx context.Context, eventID, userID uuid.UUID, answers map[string]any) (OwnParticipantAnswersView, error) {
	state, err := u.ownParticipantAnswers(ctx, eventID, userID)
	if err != nil {
		return OwnParticipantAnswersView{}, err
	}
	if !state.editable {
		return OwnParticipantAnswersView{}, participantModel.ErrParticipantFieldsLocked.Err()
	}
	// A field the participant has not filled yet (a new required one) can be
	// filled once even when it is not editable; staff-only fields never.
	editable := state.form.Form.EditableFields()
	staff := state.form.Form.StaffKeys()
	for key := range editable {
		if !staff[key] && !editable[key] && emptyAnswer(state.answers[key]) {
			editable[key] = true
		}
	}
	merged, err := participantModel.MergeEditableAnswers(editable, state.form.Form.WithoutStaffAnswers(state.answers), state.form.Form.WithoutStaffAnswers(answers))
	if err != nil {
		return OwnParticipantAnswersView{}, err
	}
	merged = state.form.Form.KeepStaffAnswers(state.answers, merged)
	bound, err := bindAnswerFiles(ctx, u.answerFiles, eventID, eventFormModel.AnswerScopeParticipant, userID, userID, state.form.Form, merged)
	if err != nil {
		return OwnParticipantAnswersView{}, err
	}
	// Without «required from everyone» a participant of an older version may
	// keep the gaps in fields that did not exist when they answered.
	validate := state.form.Form.ValidateAnswers
	if !state.form.Form.RequireExisting {
		validate = func(values map[string]any) error {
			return state.form.Form.ValidateAnswersKeepingGaps(values, state.answers)
		}
	}
	if err = validate(bound.values); err != nil {
		return OwnParticipantAnswersView{}, participantModel.ErrParticipantAnswersInvalid.WithMessage(err.Error()).Err()
	}
	saved, err := u.forms.SaveAnswer(ctx, eventFormRepo.Answer{EventID: eventID, UserID: userID, FormVersionID: state.form.ID, Values: bound.values, SubmittedAt: time.Now().UTC()})
	if err != nil {
		return OwnParticipantAnswersView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save participant form answer").Err()
	}
	if err = bound.attach(ctx, u.answerFiles, eventID, eventFormModel.AnswerScopeParticipant, userID); err != nil {
		return OwnParticipantAnswersView{}, err
	}
	if err = setParticipantFields(ctx, u.forms, eventID, userID, state.form.Form, saved.Values); err != nil {
		return OwnParticipantAnswersView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update participant fields").Err()
	}
	state.answers = saved.Values
	return state.view(), nil
}

// emptyAnswer reports an unanswered value: missing, null, blank or an empty list.
func emptyAnswer(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return typed == ""
	case []any:
		return len(typed) == 0
	}
	return false
}

type ownAnswersState struct {
	form     eventFormRepo.Version
	answers  map[string]any
	editable bool
}

func (s ownAnswersState) view() OwnParticipantAnswersView {
	answers := s.form.Form.WithoutStaffAnswers(s.answers)
	missing := missingRequired(s.form.Form, answers)
	return OwnParticipantAnswersView{
		Form: toParticipantFormView(s.form).ForParticipant(), Answers: answers, Editable: s.editable,
		Missing: missing, Blocking: blocksSubmissions(s.form.Form, missing),
	}
}

func (u *EventUseCase) ownParticipantAnswers(ctx context.Context, eventID, userID uuid.UUID) (ownAnswersState, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
		return ownAnswersState{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if err != nil || p.Status != participantModel.StatusApproved {
		return ownAnswersState{}, participantModel.ErrParticipantAccessForbidden.Err()
	}
	form, err := u.forms.Latest(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return ownAnswersState{}, eventModel.ErrParticipantFormNotFound.Err()
		}
		return ownAnswersState{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form").Err()
	}
	latest, err := u.forms.LatestRegistrationAnswers(ctx, eventID, []uuid.UUID{userID})
	if err != nil {
		return ownAnswersState{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant answers").Err()
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ownAnswersState{}, eventModel.ErrEventNotFound.Err()
		}
		return ownAnswersState{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return ownAnswersState{form: form, answers: latest[userID], editable: participantModel.AnswersEditable(e.Lifecycle.EffectiveFinishAt(), time.Now())}, nil
}
