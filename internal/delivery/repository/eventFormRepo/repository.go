package eventFormRepo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
)

type Queries interface {
	fieldPolicyQueries
	GetLatestEventFormVersion(context.Context, uuid.UUID) (postgres.EventFormVersion, error)
	GetEventFormVersionByID(context.Context, postgres.GetEventFormVersionByIDParams) (postgres.EventFormVersion, error)
	GetLatestEventFormVersionByFormID(context.Context, uuid.UUID) (postgres.EventFormVersion, error)
	CreateInitialEventForm(context.Context, postgres.CreateInitialEventFormParams) (postgres.EventFormVersion, error)
	CreateEventFormVersion(context.Context, postgres.CreateEventFormVersionParams) (postgres.EventFormVersion, error)
	PublishEventFormVersion(context.Context, postgres.PublishEventFormVersionParams) (postgres.PublishEventFormVersionRow, error)
	UpdateEventFormSettings(context.Context, postgres.UpdateEventFormSettingsParams) (int64, error)
	UpdateEventFormTitle(context.Context, postgres.UpdateEventFormTitleParams) (int64, error)
	CreateEventFormDelivery(context.Context, postgres.CreateEventFormDeliveryParams) (int64, error)
	CreateEventFormAssignment(context.Context, postgres.CreateEventFormAssignmentParams) (postgres.EventFormAssignment, error)
	GetEventFormAnswer(context.Context, postgres.GetEventFormAnswerParams) (postgres.EventFormAnswer, error)
	UpsertEventFormAnswer(context.Context, postgres.UpsertEventFormAnswerParams) (postgres.EventFormAnswer, error)
	ListEventFormAnswers(context.Context, postgres.ListEventFormAnswersParams) ([]postgres.ListEventFormAnswersRow, error)
	ListEventFormAssignmentsByTrigger(context.Context, postgres.ListEventFormAssignmentsByTriggerParams) ([]postgres.EventFormAssignment, error)
	ListEventForms(context.Context, uuid.UUID) ([]postgres.ListEventFormsRow, error)
	GetEventForm(context.Context, postgres.GetEventFormParams) (postgres.GetEventFormRow, error)
	GetEventFormDelivery(context.Context, postgres.GetEventFormDeliveryParams) (postgres.EventFormDelivery, error)
	ListEventFormDeliveries(context.Context, postgres.ListEventFormDeliveriesParams) ([]postgres.EventFormDelivery, error)
	ListPendingEventFormDeliveries(context.Context, postgres.ListPendingEventFormDeliveriesParams) ([]postgres.ListPendingEventFormDeliveriesRow, error)
	CompleteEventFormDelivery(context.Context, postgres.CompleteEventFormDeliveryParams) (int64, error)
	ListActiveFutureTimedEventFormAssignments(context.Context, uuid.UUID) ([]postgres.EventFormAssignment, error)
	ListDueTimedEventFormAssignmentsForUpdate(context.Context, postgres.ListDueTimedEventFormAssignmentsForUpdateParams) ([]postgres.EventFormAssignment, error)
	MarkEventFormAssignmentMaterialized(context.Context, postgres.MarkEventFormAssignmentMaterializedParams) (int64, error)
	HasIncompleteRequiredEventFormDelivery(context.Context, postgres.HasIncompleteRequiredEventFormDeliveryParams) (bool, error)
	ListEventFormRecipientCandidates(context.Context, uuid.UUID) ([]postgres.ListEventFormRecipientCandidatesRow, error)
	ListLatestRegistrationAnswersForUsers(ctx context.Context, arg postgres.ListLatestRegistrationAnswersForUsersParams) ([]postgres.ListLatestRegistrationAnswersForUsersRow, error)
}

type RecipientCandidate struct {
	UserID          uuid.UUID
	TeamID          *uuid.UUID
	TeamRole        *int16
	TeamMemberCount int32
}

type Version struct {
	ID, FormID, EventID uuid.UUID
	Form                eventFormModel.Form
	CreatedAt           time.Time
}
type Form struct {
	ID, EventID          uuid.UUID
	Title                string
	Enabled, Required    bool
	Current              Version
	CreatedAt, UpdatedAt time.Time
}

type PendingDelivery struct {
	Form          Form
	FormVersionID uuid.UUID
	UserID        uuid.UUID
	AssignmentID  uuid.UUID
	Presentation  eventFormModel.Presentation
	Dismissible   bool
	Gates         []eventFormModel.Capability
	CreatedAt     time.Time
}
type Answer struct {
	EventID, UserID, FormVersionID uuid.UUID
	FormID                         uuid.UUID
	Version                        int32
	Document                       eventContentModel.Document
	Name, Email                    string
	Values                         map[string]any
	SubmittedAt                    time.Time
}

// Assignment is the persisted rule needed by the signal hook. It deliberately
// keeps the storage identity separate from the validated domain configuration.
type Assignment struct {
	ID, EventID, FormID uuid.UUID
	Rule                eventFormModel.Assignment
}

type Delivery struct {
	FormVersionID, UserID, AssignmentID uuid.UUID
	Presentation                        eventFormModel.Presentation
	Dismissible                         bool
	Gates                               []eventFormModel.Capability
	CompletedAt                         *time.Time
	CreatedAt                           time.Time
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Latest(ctx context.Context, eventID uuid.UUID) (Version, error) {
	row, err := r.q.GetLatestEventFormVersion(ctx, eventID)
	if err != nil {
		return Version{}, err
	}
	return versionToDomain(row)
}

// LatestByFormID resolves the immutable version that a newly materialized
// delivery must require. A delivery keeps that version even if moderators
// later publish a newer one.
func (r *Repository) LatestByFormID(ctx context.Context, formID uuid.UUID) (Version, error) {
	row, err := r.q.GetLatestEventFormVersionByFormID(ctx, formID)
	if err != nil {
		return Version{}, err
	}
	return versionToDomain(row)
}

func (r *Repository) GetVersion(ctx context.Context, eventID, versionID uuid.UUID) (Version, error) {
	row, err := r.q.GetEventFormVersionByID(ctx, postgres.GetEventFormVersionByIDParams{EventID: eventID, ID: versionID})
	if err != nil {
		return Version{}, err
	}
	return versionToDomain(row)
}

func (r *Repository) ListForms(ctx context.Context, eventID uuid.UUID) ([]Form, error) {
	rows, err := r.q.ListEventForms(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]Form, 0, len(rows))
	for _, row := range rows {
		value, err := formFromRow(row.ID, row.EventID, row.Title, row.Enabled, row.Required, row.CreatedAt, row.UpdatedAt, row.CurrentVersionID, row.CurrentVersion, row.CurrentDocument, row.VersionCreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func (r *Repository) GetForm(ctx context.Context, eventID, formID uuid.UUID) (Form, error) {
	row, err := r.q.GetEventForm(ctx, postgres.GetEventFormParams{EventID: eventID, FormID: formID})
	if err != nil {
		return Form{}, err
	}
	return formFromRow(row.ID, row.EventID, row.Title, row.Enabled, row.Required, row.CreatedAt, row.UpdatedAt, row.CurrentVersionID, row.CurrentVersion, row.CurrentDocument, row.VersionCreatedAt)
}

func (r *Repository) UpdateFormTitle(ctx context.Context, eventID, formID uuid.UUID, title string, at time.Time) error {
	_, err := r.q.UpdateEventFormTitle(ctx, postgres.UpdateEventFormTitleParams{EventID: eventID, FormID: formID, Title: title, UpdatedAt: at})
	return err
}

// PublishVersion atomically creates an immutable version and moves the stable
// form's manager-facing current settings to that exact version.
func (r *Repository) PublishVersion(ctx context.Context, value Version, title string) (Version, error) {
	document, err := json.Marshal(value.Form.Document)
	if err != nil {
		return Version{}, err
	}
	row, err := r.q.PublishEventFormVersion(ctx, postgres.PublishEventFormVersionParams{
		ID: value.ID, FormID: value.FormID, EventID: value.EventID, Version: value.Form.Version,
		Enabled: value.Form.Enabled, Required: value.Form.Required, Document: document,
		CreatedAt: value.CreatedAt, UpdatedAt: value.CreatedAt, Title: title,
	})
	if err != nil {
		return Version{}, err
	}
	return versionToDomain(postgres.EventFormVersion{ID: row.ID, EventID: row.EventID, FormID: row.FormID, Version: row.Version, Enabled: row.Enabled, Required: row.Required, Document: row.Document, CreatedAt: row.CreatedAt})
}

func (r *Repository) CreateForm(ctx context.Context, id, versionID, eventID uuid.UUID, title string, value eventFormModel.Form, at time.Time) (Form, error) {
	document, err := json.Marshal(value.Document)
	if err != nil {
		return Form{}, err
	}
	row, err := r.q.CreateInitialEventForm(ctx, postgres.CreateInitialEventFormParams{ID: versionID, FormID: id, EventID: eventID, Title: title, Purpose: "other", Version: value.Version, Enabled: value.Enabled, Required: value.Required, Document: document, CreatedAt: at})
	if err != nil {
		return Form{}, err
	}
	version, err := versionToDomain(row)
	if err != nil {
		return Form{}, err
	}
	return Form{ID: id, EventID: eventID, Title: title, Enabled: value.Enabled, Required: value.Required, Current: version, CreatedAt: at, UpdatedAt: at}, nil
}

func formFromRow(id, eventID uuid.UUID, title string, enabled, required bool, createdAt, updatedAt time.Time, versionID uuid.UUID, version int32, document []byte, versionCreatedAt time.Time) (Form, error) {
	var doc eventContentModel.Document
	if err := json.Unmarshal(document, &doc); err != nil {
		return Form{}, err
	}
	return Form{ID: id, EventID: eventID, Title: title, Enabled: enabled, Required: required, Current: Version{ID: versionID, FormID: id, EventID: eventID, Form: eventFormModel.Form{Version: version, Enabled: enabled, Required: required, Document: doc}, CreatedAt: versionCreatedAt}, CreatedAt: createdAt, UpdatedAt: updatedAt}, nil
}
func (r *Repository) Create(ctx context.Context, value Version) (Version, error) {
	document, err := json.Marshal(value.Form.Document)
	if err != nil {
		return Version{}, err
	}
	formID := value.FormID
	if formID == uuid.Nil {
		formID = value.ID
	}
	params := postgres.CreateEventFormVersionParams{ID: value.ID, FormID: formID, EventID: value.EventID, Version: value.Form.Version, Enabled: value.Form.Enabled, Required: value.Form.Required, Document: document, CreatedAt: value.CreatedAt, RequireExisting: value.Form.RequireExisting, BlockSubmissions: value.Form.BlockSubmissions}
	var row postgres.EventFormVersion
	if value.FormID == uuid.Nil {
		row, err = r.q.CreateInitialEventForm(ctx, postgres.CreateInitialEventFormParams{
			ID: value.ID, FormID: formID, EventID: value.EventID, Version: value.Form.Version,
			Enabled: value.Form.Enabled, Required: value.Form.Required, Document: document,
			RequireExisting: value.Form.RequireExisting, BlockSubmissions: value.Form.BlockSubmissions,
			CreatedAt: value.CreatedAt, Title: "Registration form", Purpose: "registration",
		})
	} else {
		row, err = r.q.CreateEventFormVersion(ctx, params)
	}
	if err != nil {
		return Version{}, err
	}
	if value.FormID != uuid.Nil {
		if _, err = r.q.UpdateEventFormSettings(ctx, postgres.UpdateEventFormSettingsParams{FormID: formID, Enabled: value.Form.Enabled, Required: value.Form.Required, UpdatedAt: value.CreatedAt}); err != nil {
			return Version{}, err
		}
	}
	return versionToDomain(row)
}
func (r *Repository) Answer(ctx context.Context, eventID, userID, formVersionID uuid.UUID) (Answer, error) {
	row, err := r.q.GetEventFormAnswer(ctx, postgres.GetEventFormAnswerParams{EventID: eventID, UserID: userID, FormVersionID: formVersionID})
	if err != nil {
		return Answer{}, err
	}
	return answerToDomain(row)
}
func (r *Repository) SaveAnswer(ctx context.Context, value Answer) (Answer, error) {
	answers, err := json.Marshal(value.Values)
	if err != nil {
		return Answer{}, err
	}
	row, err := r.q.UpsertEventFormAnswer(ctx, postgres.UpsertEventFormAnswerParams{EventID: value.EventID, UserID: value.UserID, FormVersionID: value.FormVersionID, Answers: answers, SubmittedAt: value.SubmittedAt})
	if err != nil {
		return Answer{}, err
	}
	return answerToDomain(row)
}

func (r *Repository) ListAnswers(ctx context.Context, eventID, formID uuid.UUID) ([]Answer, error) {
	rows, err := r.q.ListEventFormAnswers(ctx, postgres.ListEventFormAnswersParams{EventID: eventID, FormID: formID})
	if err != nil {
		return nil, err
	}
	answers := make([]Answer, 0, len(rows))
	for _, row := range rows {
		var values map[string]any
		if err = json.Unmarshal(row.Answers, &values); err != nil {
			return nil, err
		}
		var document eventContentModel.Document
		if err = json.Unmarshal(row.Document, &document); err != nil {
			return nil, err
		}
		answers = append(answers, Answer{EventID: row.EventID, UserID: row.UserID, FormVersionID: row.FormVersionID, FormID: row.FormID, Version: row.Version, Document: document, Name: row.Name, Email: row.Email, Values: values, SubmittedAt: row.SubmittedAt})
	}
	return answers, nil
}

func (r *Repository) ListAssignmentsByTrigger(ctx context.Context, eventID uuid.UUID, trigger eventFormModel.Trigger) ([]Assignment, error) {
	rows, err := r.q.ListEventFormAssignmentsByTrigger(ctx, postgres.ListEventFormAssignmentsByTriggerParams{EventID: eventID, Trigger: string(trigger)})
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		assignment, err := assignmentToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, assignment)
	}
	return out, nil
}

// ListActiveFutureTimedAssignments also includes a manual assignment after its
// initial audience snapshot. Both use the enrollment signal only when their
// explicit include-future flag is set.
func (r *Repository) ListActiveFutureTimedAssignments(ctx context.Context, eventID uuid.UUID) ([]Assignment, error) {
	rows, err := r.q.ListActiveFutureTimedEventFormAssignments(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		assignment, err := assignmentToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, assignment)
	}
	return out, nil
}

// ListDueTimedAssignmentsForUpdate must be used inside the caller's unit of
// work. The row locks make the subsequent delivery writes and materialization
// marker one all-or-nothing operation across backend replicas.
func (r *Repository) ListDueTimedAssignmentsForUpdate(ctx context.Context, now time.Time, limit int32) ([]Assignment, error) {
	rows, err := r.q.ListDueTimedEventFormAssignmentsForUpdate(ctx, postgres.ListDueTimedEventFormAssignmentsForUpdateParams{NowAt: pgtype.Timestamptz{Time: now, Valid: true}, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		assignment, err := assignmentToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, assignment)
	}
	return out, nil
}

func (r *Repository) MarkAssignmentMaterialized(ctx context.Context, assignmentID uuid.UUID, at time.Time) error {
	_, err := r.q.MarkEventFormAssignmentMaterialized(ctx, postgres.MarkEventFormAssignmentMaterializedParams{ID: assignmentID, MaterializedAt: pgtype.Timestamptz{Time: at, Valid: true}})
	return err
}

func (r *Repository) HasIncompleteRequiredDelivery(ctx context.Context, eventID, userID uuid.UUID, capability eventFormModel.Capability) (bool, error) {
	return r.q.HasIncompleteRequiredEventFormDelivery(ctx, postgres.HasIncompleteRequiredEventFormDeliveryParams{EventID: eventID, UserID: userID, Capability: []byte(capability)})
}

func assignmentToDomain(row postgres.EventFormAssignment) (Assignment, error) {
	var audience eventFormModel.Audience
	var gates []eventFormModel.Capability
	if err := json.Unmarshal(row.Audience, &audience); err != nil {
		return Assignment{}, fmt.Errorf("decode form audience: %w", err)
	}
	if err := json.Unmarshal(row.Gates, &gates); err != nil {
		return Assignment{}, fmt.Errorf("decode form gates: %w", err)
	}
	rule := eventFormModel.Assignment{Trigger: eventFormModel.Trigger(row.Trigger), Audience: audience, Presentation: eventFormModel.Presentation(row.Presentation), Dismissible: row.Dismissible, Gates: gates}
	if row.At.Valid {
		at := row.At.Time
		rule.At = &at
	}
	if err := rule.Validate(); err != nil {
		return Assignment{}, err
	}
	return Assignment{ID: row.ID, EventID: row.EventID, FormID: row.FormID, Rule: rule}, nil
}

// CreateDelivery is safe for replay: a pre-existing form-version/user pair
// returns false without rewriting its pending/completed state.
func (r *Repository) CreateDelivery(ctx context.Context, value Delivery) (bool, error) {
	gates, err := json.Marshal(value.Gates)
	if err != nil {
		return false, err
	}
	affected, err := r.q.CreateEventFormDelivery(ctx, postgres.CreateEventFormDeliveryParams{
		FormVersionID: value.FormVersionID, UserID: value.UserID, AssignmentID: value.AssignmentID,
		Presentation: string(value.Presentation), Dismissible: value.Dismissible, Gates: gates, CreatedAt: value.CreatedAt,
	})
	return affected == 1, err
}

func (r *Repository) CreateAssignment(ctx context.Context, value Assignment, includeFuture bool, enabled bool, at time.Time) (Assignment, error) {
	if err := value.Rule.Validate(); err != nil {
		return Assignment{}, err
	}
	audience, err := json.Marshal(value.Rule.Audience)
	if err != nil {
		return Assignment{}, err
	}
	gates, err := json.Marshal(value.Rule.Gates)
	if err != nil {
		return Assignment{}, err
	}
	var scheduledAt pgtype.Timestamptz
	if value.Rule.At != nil {
		scheduledAt = pgtype.Timestamptz{Time: *value.Rule.At, Valid: true}
	}
	row, err := r.q.CreateEventFormAssignment(ctx, postgres.CreateEventFormAssignmentParams{ID: value.ID, EventID: value.EventID, FormID: value.FormID, Trigger: string(value.Rule.Trigger), Audience: audience, IncludeFutureParticipants: includeFuture, Presentation: string(value.Rule.Presentation), Dismissible: value.Rule.Dismissible, Gates: gates, At: scheduledAt, Enabled: enabled, CreatedAt: at, UpdatedAt: at})
	if err != nil {
		return Assignment{}, err
	}
	return assignmentToDomain(row)
}

func (r *Repository) GetDelivery(ctx context.Context, eventID, formID, userID uuid.UUID) (Delivery, error) {
	row, err := r.q.GetEventFormDelivery(ctx, postgres.GetEventFormDeliveryParams{EventID: eventID, FormID: formID, UserID: userID})
	if err != nil {
		return Delivery{}, err
	}
	return deliveryToDomain(row)
}

func (r *Repository) ListDeliveries(ctx context.Context, eventID, formID uuid.UUID) ([]Delivery, error) {
	rows, err := r.q.ListEventFormDeliveries(ctx, postgres.ListEventFormDeliveriesParams{EventID: eventID, FormID: formID})
	if err != nil {
		return nil, err
	}
	out := make([]Delivery, 0, len(rows))
	for _, row := range rows {
		value, err := deliveryToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, nil
}

func (r *Repository) ListPendingDeliveries(ctx context.Context, eventID, userID uuid.UUID) ([]PendingDelivery, error) {
	rows, err := r.q.ListPendingEventFormDeliveries(ctx, postgres.ListPendingEventFormDeliveriesParams{EventID: eventID, UserID: userID})
	if err != nil {
		return nil, err
	}
	out := make([]PendingDelivery, 0, len(rows))
	for _, row := range rows {
		var gates []eventFormModel.Capability
		if err := json.Unmarshal(row.Gates, &gates); err != nil {
			return nil, err
		}
		form, err := formFromRow(row.FormID, eventID, row.Title, row.FormEnabled, row.FormRequired, row.CreatedAt, row.CreatedAt, row.FormVersionID, row.Version, row.Document, row.CreatedAt)
		if err != nil {
			return nil, err
		}
		out = append(out, PendingDelivery{Form: form, FormVersionID: row.FormVersionID, UserID: row.UserID, AssignmentID: row.AssignmentID, Presentation: eventFormModel.Presentation(row.Presentation), Dismissible: row.Dismissible, Gates: gates, CreatedAt: row.CreatedAt})
	}
	return out, nil
}

func (r *Repository) CompleteDelivery(ctx context.Context, formVersionID, userID uuid.UUID, completedAt time.Time) (bool, error) {
	affected, err := r.q.CompleteEventFormDelivery(ctx, postgres.CompleteEventFormDeliveryParams{FormVersionID: formVersionID, UserID: userID, CompletedAt: pgtype.Timestamptz{Time: completedAt, Valid: true}})
	return affected == 1, err
}

func (r *Repository) ListRecipientCandidates(ctx context.Context, eventID uuid.UUID) ([]RecipientCandidate, error) {
	rows, err := r.q.ListEventFormRecipientCandidates(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]RecipientCandidate, 0, len(rows))
	for _, row := range rows {
		candidate := RecipientCandidate{UserID: row.UserID, TeamMemberCount: row.TeamMemberCount}
		if row.TeamID.Valid {
			teamID := row.TeamID.UUID
			candidate.TeamID = &teamID
		}
		if row.TeamRole.Valid {
			role := row.TeamRole.Int16
			candidate.TeamRole = &role
		}
		out = append(out, candidate)
	}
	return out, nil
}

func versionToDomain(row postgres.EventFormVersion) (Version, error) {
	var doc eventContentModel.Document
	if err := json.Unmarshal(row.Document, &doc); err != nil {
		return Version{}, err
	}
	return Version{ID: row.ID, FormID: row.FormID, EventID: row.EventID, Form: eventFormModel.Form{Version: row.Version, Enabled: row.Enabled, Required: row.Required, Document: doc, RequireExisting: row.RequireExisting, BlockSubmissions: row.BlockSubmissions}, CreatedAt: row.CreatedAt}, nil
}
func answerToDomain(row postgres.EventFormAnswer) (Answer, error) {
	var values map[string]any
	if err := json.Unmarshal(row.Answers, &values); err != nil {
		return Answer{}, err
	}
	return Answer{EventID: row.EventID, UserID: row.UserID, FormVersionID: row.FormVersionID, Values: values, SubmittedAt: row.SubmittedAt}, nil
}

func deliveryToDomain(row postgres.EventFormDelivery) (Delivery, error) {
	var gates []eventFormModel.Capability
	if err := json.Unmarshal(row.Gates, &gates); err != nil {
		return Delivery{}, err
	}
	value := Delivery{FormVersionID: row.FormVersionID, UserID: row.UserID, AssignmentID: row.AssignmentID, Presentation: eventFormModel.Presentation(row.Presentation), Dismissible: row.Dismissible, Gates: gates, CreatedAt: row.CreatedAt}
	if row.CompletedAt.Valid {
		completedAt := row.CompletedAt.Time
		value.CompletedAt = &completedAt
	}
	return value, nil
}

// LatestRegistrationAnswers returns each listed user's newest registration
// form answers in one query (moderation list columns). Users without an
// answer are absent from the map.
func (r *Repository) LatestRegistrationAnswers(ctx context.Context, eventID uuid.UUID, userIDs []uuid.UUID) (map[uuid.UUID]map[string]any, error) {
	out := make(map[uuid.UUID]map[string]any, len(userIDs))
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListLatestRegistrationAnswersForUsers(ctx, postgres.ListLatestRegistrationAnswersForUsersParams{EventID: eventID, UserIds: userIDs})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var values map[string]any
		if err = json.Unmarshal(row.Answers, &values); err != nil {
			return nil, err
		}
		out[row.UserID] = values
	}
	return out, nil
}
