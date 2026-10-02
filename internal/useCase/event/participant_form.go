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

func (u *EventUseCase) ConfigureParticipantForm(ctx context.Context, eventID uuid.UUID, in ConfigureParticipantFormInput) (ParticipantFormView, error) {
	form := eventFormModel.Form{Enabled: in.Enabled, Required: in.Required, Document: in.Document}
	if err := form.Validate(); err != nil {
		return ParticipantFormView{}, err
	}
	version := int32(1)
	formID := uuid.Nil
	var previous eventFormModel.Form
	if latest, err := u.forms.Latest(ctx, eventID); err == nil {
		version = latest.Form.Version + 1
		formID = latest.FormID
		previous = latest.Form
	} else if !repositoryTools.IsObjectNotFoundError(err) && err != pgx.ErrNoRows {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form").Err()
	}
	form.Version = version
	form.RequireExisting, form.BlockSubmissions = inheritPolicy(previous, in.RequireExisting, in.BlockSubmissions)
	created, err := u.forms.Create(ctx, eventFormRepo.Version{ID: uuid.Must(uuid.NewV7()), FormID: formID, EventID: eventID, Form: form, CreatedAt: time.Now().UTC()})
	if err != nil {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create participant form version").Err()
	}
	if err = recountParticipantFields(ctx, u.forms, eventID, created.Form); err != nil {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to recount participant fields").Err()
	}
	return u.participantFormView(ctx, eventID, created)
}

func (u *EventUseCase) GetParticipantForm(ctx context.Context, eventID uuid.UUID) (ParticipantFormView, error) {
	value, err := u.forms.Latest(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return ParticipantFormView{}, eventModel.ErrParticipantFormNotFound.Err()
		}
		return ParticipantFormView{}, err
	}
	return u.participantFormView(ctx, eventID, value)
}

// participantFormView adds how many participants already answered, which the
// editor needs to offer «required from everyone» for a new required field.
func (u *EventUseCase) participantFormView(ctx context.Context, eventID uuid.UUID, value eventFormRepo.Version) (ParticipantFormView, error) {
	view := toParticipantFormView(value)
	answered, err := u.forms.CountAnswered(ctx, eventID)
	if err != nil {
		return ParticipantFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count participant answers").Err()
	}
	view.Answered = answered
	return view, nil
}

func (u *EventUseCase) SubmitParticipantForm(ctx context.Context, eventID, userID uuid.UUID, in SubmitParticipantFormInput) (ParticipantFormAnswerView, error) {
	form, err := u.forms.Latest(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return ParticipantFormAnswerView{}, eventModel.ErrParticipantFormNotFound.Err()
		}
		return ParticipantFormAnswerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form").Err()
	}
	// The form is answered while the event lasts: after its effective finish the answers are frozen, as
	// they are for the participant's own edits.
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ParticipantFormAnswerView{}, eventModel.ErrEventNotFound.Err()
		}
		return ParticipantFormAnswerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if !participantModel.AnswersEditable(e.Lifecycle.EffectiveFinishAt(), time.Now()) {
		return ParticipantFormAnswerView{}, participantModel.ErrParticipantFieldsLocked.Err()
	}
	// A person the organizers rejected does not keep writing into the event's data. Someone who has
	// not applied yet has no row: the form is part of applying.
	if p, getErr := u.participants.Get(ctx, eventID, userID); getErr == nil {
		if p.Status == participantModel.StatusRejected {
			return ParticipantFormAnswerView{}, participantModel.ErrParticipantAccessForbidden.Err()
		}
	} else if !repositoryTools.IsObjectNotFoundError(getErr) && getErr != pgx.ErrNoRows {
		return ParticipantFormAnswerView{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event participant").Err()
	}
	// Answers an organizer prefilled stay: a non-editable field with a value
	// cannot be changed by the participant.
	stored, err := u.forms.LatestRegistrationAnswers(ctx, eventID, []uuid.UUID{userID})
	if err != nil {
		return ParticipantFormAnswerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant answers").Err()
	}
	// Participants never write staff-only fields; the ones organizers
	// recorded stay as they are.
	answers, err := participantModel.LockFilledAnswers(form.Form.EditableFields(), stored[userID], form.Form.WithoutStaffAnswers(in.Answers))
	if err != nil {
		return ParticipantFormAnswerView{}, err
	}
	answers = form.Form.KeepStaffAnswers(stored[userID], answers)
	bound, err := bindAnswerFiles(ctx, u.answerFiles, eventID, eventFormModel.AnswerScopeParticipant, userID, userID, form.Form, answers)
	if err != nil {
		return ParticipantFormAnswerView{}, err
	}
	if err = form.Form.ValidateAnswers(bound.values); err != nil {
		return ParticipantFormAnswerView{}, err
	}
	answer, err := u.forms.SaveAnswer(ctx, eventFormRepo.Answer{EventID: eventID, UserID: userID, FormVersionID: form.ID, Values: bound.values, SubmittedAt: time.Now().UTC()})
	if err != nil {
		return ParticipantFormAnswerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save participant form answer").Err()
	}
	if err = bound.attach(ctx, u.answerFiles, eventID, eventFormModel.AnswerScopeParticipant, userID); err != nil {
		return ParticipantFormAnswerView{}, err
	}
	if err = setParticipantFields(ctx, u.forms, eventID, userID, form.Form, answer.Values); err != nil {
		return ParticipantFormAnswerView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update participant fields").Err()
	}
	participantForm := form.Form.ForParticipant()
	return ParticipantFormAnswerView{UserID: answer.UserID, FormVersion: form.Form.Version, Answers: form.Form.WithoutStaffAnswers(answer.Values), Document: participantForm.Document, SubmittedAt: answer.SubmittedAt}, nil
}

func (u *EventUseCase) ListParticipantFormAnswers(ctx context.Context, eventID uuid.UUID) ([]ParticipantFormAnswerView, error) {
	registration, err := u.forms.Latest(ctx, eventID)
	if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
		return []ParticipantFormAnswerView{}, nil
	}
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant form").Err()
	}
	answers, err := u.forms.ListAnswers(ctx, eventID, registration.FormID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list participant form answers").Err()
	}
	out := make([]ParticipantFormAnswerView, 0, len(answers))
	for _, answer := range answers {
		out = append(out, ParticipantFormAnswerView{UserID: answer.UserID, Name: answer.Name, Email: answer.Email, FormVersion: answer.Version, Answers: answer.Values, Document: answer.Document, SubmittedAt: answer.SubmittedAt})
	}
	return out, nil
}

func toParticipantFormView(value eventFormRepo.Version) ParticipantFormView {
	return ParticipantFormView{Version: value.Form.Version, Enabled: value.Form.Enabled, Required: value.Form.Required, Document: value.Form.Document,
		RequireExisting: value.Form.RequireExisting, BlockSubmissions: value.Form.BlockSubmissions}
}
