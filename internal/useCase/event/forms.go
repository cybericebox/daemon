package event

import (
	"context"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	eventFormRepo "github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

func (u *EventUseCase) CreateEventForm(ctx context.Context, eventID uuid.UUID, in CreateEventFormInput) (EventFormView, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return EventFormView{}, model.ErrPlatform.WithMessage("Event form title is required").Err()
	}
	form := eventFormModel.Form{Version: 1, Enabled: in.Enabled, Required: in.Required, Document: in.Document}
	if err := validateSurveyForm(form); err != nil {
		return EventFormView{}, err
	}
	now := time.Now().UTC()
	created, err := u.forms.CreateForm(ctx, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), eventID, title, form, now)
	if err != nil {
		return EventFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create event form").Err()
	}
	return toEventFormView(created), nil
}

func (u *EventUseCase) ListEventForms(ctx context.Context, eventID uuid.UUID) ([]EventFormView, error) {
	forms, err := u.forms.ListForms(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event forms").Err()
	}
	out := make([]EventFormView, 0, len(forms))
	for _, form := range forms {
		out = append(out, toEventFormView(form))
	}
	return out, nil
}

func (u *EventUseCase) GetEventForm(ctx context.Context, eventID, formID uuid.UUID) (EventFormView, error) {
	form, err := u.forms.GetForm(ctx, eventID, formID)
	if err != nil {
		return EventFormView{}, err
	}
	return toEventFormView(form), nil
}

// GetOwnEventForm exposes only a form delivery owned by the participant. The
// delivery pins an immutable version, so a later moderator edit cannot change
// the document a participant was asked to complete.
func (u *EventUseCase) GetOwnEventForm(ctx context.Context, eventID, formID, userID uuid.UUID) (EventFormView, error) {
	delivery, err := u.forms.GetDelivery(ctx, eventID, formID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventFormView{}, model.ErrPlatform.WithMessage("Event form is not assigned to this participant").Err()
		}
		return EventFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event form delivery").Err()
	}
	form, err := u.forms.GetForm(ctx, eventID, formID)
	if err != nil {
		return EventFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event form").Err()
	}
	version, err := u.forms.GetVersion(ctx, eventID, delivery.FormVersionID)
	if err != nil {
		return EventFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get delivered event form version").Err()
	}
	view := toEventFormView(form)
	view.Enabled = version.Form.Enabled
	view.Required = version.Form.Required
	view.CurrentVersionID = version.ID
	view.Version = version.Form.Version
	view.Document = version.Form.Document
	return view, nil
}

// UpdateEventForm publishes a new immutable document version. Existing
// deliveries retain their prior version and answers remain interpretable.
func (u *EventUseCase) UpdateEventForm(ctx context.Context, eventID, formID uuid.UUID, in CreateEventFormInput) (EventFormView, error) {
	current, err := u.forms.GetForm(ctx, eventID, formID)
	if err != nil {
		return EventFormView{}, err
	}
	title := strings.TrimSpace(in.Title)
	if title == "" {
		return EventFormView{}, model.ErrPlatform.WithMessage("Event form title is required").Err()
	}
	next := eventFormModel.Form{Version: current.Current.Form.Version + 1, Enabled: in.Enabled, Required: in.Required, Document: in.Document}
	if err = validateSurveyForm(next); err != nil {
		return EventFormView{}, err
	}
	now := time.Now().UTC()
	if _, err = u.forms.PublishVersion(ctx, eventFormRepo.Version{ID: uuid.Must(uuid.NewV7()), FormID: formID, EventID: eventID, Form: next, CreatedAt: now}, title); err != nil {
		return EventFormView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to version event form").Err()
	}
	return u.GetEventForm(ctx, eventID, formID)
}

func (u *EventUseCase) ListEventFormAnswers(ctx context.Context, eventID, formID uuid.UUID) ([]EventFormAnswerView, error) {
	if _, err := u.forms.GetForm(ctx, eventID, formID); err != nil {
		return nil, err
	}
	answers, err := u.forms.ListAnswers(ctx, eventID, formID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event form answers").Err()
	}
	result := make([]EventFormAnswerView, 0, len(answers))
	for _, answer := range answers {
		result = append(result, EventFormAnswerView{UserID: answer.UserID, Name: answer.Name, Email: answer.Email, FormVersionID: answer.FormVersionID, Version: answer.Version, Answers: answer.Values, Document: answer.Document, SubmittedAt: answer.SubmittedAt})
	}
	return result, nil
}

func (u *EventUseCase) ListEventFormDeliveries(ctx context.Context, eventID, formID uuid.UUID) ([]EventFormDeliveryView, error) {
	if _, err := u.forms.GetForm(ctx, eventID, formID); err != nil {
		return nil, err
	}
	deliveries, err := u.forms.ListDeliveries(ctx, eventID, formID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event form deliveries").Err()
	}
	result := make([]EventFormDeliveryView, 0, len(deliveries))
	for _, delivery := range deliveries {
		result = append(result, EventFormDeliveryView{FormVersionID: delivery.FormVersionID, UserID: delivery.UserID, AssignmentID: delivery.AssignmentID, Presentation: delivery.Presentation, Dismissible: delivery.Dismissible, Gates: delivery.Gates, CompletedAt: delivery.CompletedAt, CreatedAt: delivery.CreatedAt})
	}
	return result, nil
}

func (u *EventUseCase) AssignEventForm(ctx context.Context, eventID, formID uuid.UUID, in CreateEventFormAssignmentInput) error {
	if _, err := u.forms.GetForm(ctx, eventID, formID); err != nil {
		return err
	}
	assignment, err := u.forms.CreateAssignment(ctx, eventFormRepo.Assignment{ID: uuid.Must(uuid.NewV7()), EventID: eventID, FormID: formID, Rule: in.Rule}, in.IncludeFuture, in.Enabled, time.Now().UTC())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to assign event form").Err()
	}
	if in.Rule.Trigger == eventFormModel.TriggerManual && in.Enabled {
		version, err := u.forms.LatestByFormID(ctx, formID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to resolve event form version for manual delivery").Err()
		}
		store := NewFormDeliveryRepositoryStore(u.forms, time.Now)
		if err = MaterializeFormDeliveries(ctx, store, eventID, FormDeliveryAssignment{ID: assignment.ID, FormVersionID: version.ID, Assignment: assignment.Rule}, nil); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to deliver event form").Err()
		}
		if err = u.forms.MarkAssignmentMaterialized(ctx, assignment.ID, time.Now().UTC()); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to record event form delivery snapshot").Err()
		}
	}
	return nil
}

func (u *EventUseCase) ListPendingEventForms(ctx context.Context, eventID, userID uuid.UUID) ([]PendingEventFormView, error) {
	rows, err := u.forms.ListPendingDeliveries(ctx, eventID, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list pending event forms").Err()
	}
	out := make([]PendingEventFormView, 0, len(rows))
	for _, row := range rows {
		out = append(out, PendingEventFormView{Form: toEventFormView(row.Form), FormVersionID: row.FormVersionID, Presentation: row.Presentation, Dismissible: row.Dismissible, Gates: row.Gates, CreatedAt: row.CreatedAt})
	}
	return out, nil
}

func (u *EventUseCase) SubmitEventFormResponse(ctx context.Context, eventID, formID, userID uuid.UUID, in SubmitEventFormResponseInput) error {
	delivery, err := u.forms.GetDelivery(ctx, eventID, formID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return model.ErrPlatform.WithMessage("Event form is not assigned to this participant").Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event form delivery").Err()
	}
	if delivery.FormVersionID != in.FormVersionID {
		return model.ErrPlatform.WithMessage("Event form version does not match its delivery").Err()
	}
	version, err := u.forms.GetVersion(ctx, eventID, in.FormVersionID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get delivered event form version").Err()
	}
	if err = version.Form.ValidateAnswers(in.Answers); err != nil {
		return participantAnswersError(err)
	}
	now := time.Now().UTC()
	if _, err = u.forms.SaveAnswer(ctx, eventFormRepo.Answer{EventID: eventID, UserID: userID, FormVersionID: version.ID, Values: in.Answers, SubmittedAt: now}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save event form response").Err()
	}
	if _, err = u.forms.CompleteDelivery(ctx, version.ID, userID, now); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to complete event form delivery").Err()
	}
	return nil
}

func toEventFormView(value eventFormRepo.Form) EventFormView {
	return EventFormView{ID: value.ID, EventID: value.EventID, Title: value.Title, Enabled: value.Enabled, Required: value.Required, CurrentVersionID: value.Current.ID, Version: value.Current.Form.Version, Document: value.Current.Form.Document, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt}
}

// validateSurveyForm checks a survey: file questions exist only in the
// participant form and team fields, where uploads are bound to their answers.
func validateSurveyForm(form eventFormModel.Form) error {
	if len(form.FileFields()) > 0 {
		return participantModel.ErrParticipantFormInvalid.WithMessage("survey forms cannot have file questions").Err()
	}
	if err := form.Validate(); err != nil {
		return participantModel.ErrParticipantFormInvalid.WithMessage(err.Error()).Err()
	}
	return nil
}
