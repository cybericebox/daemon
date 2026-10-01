package eventFormRepo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// fieldPolicyQueries are the reads and writes behind «required for everyone»
// and staff-only fields (see the field-policy notes on event_form_versions).
type fieldPolicyQueries interface {
	ListEventParticipantRegistrationAnswers(ctx context.Context, eventID uuid.UUID) ([]postgres.ListEventParticipantRegistrationAnswersRow, error)
	SetEventParticipantsFieldsMissing(ctx context.Context, arg postgres.SetEventParticipantsFieldsMissingParams) error
	GetEventParticipantFieldsMissing(ctx context.Context, arg postgres.GetEventParticipantFieldsMissingParams) (int32, error)
	CountEventRegistrationAnswers(ctx context.Context, eventID uuid.UUID) (int64, error)
	GetLatestEventRegistrationAnswerRow(ctx context.Context, arg postgres.GetLatestEventRegistrationAnswerRowParams) (postgres.GetLatestEventRegistrationAnswerRowRow, error)
	UpdateEventFormAnswerValues(ctx context.Context, arg postgres.UpdateEventFormAnswerValuesParams) (int64, error)
	InsertEventStaffFieldChange(ctx context.Context, arg postgres.InsertEventStaffFieldChangeParams) error
	GetLastEventStaffFieldChange(ctx context.Context, arg postgres.GetLastEventStaffFieldChangeParams) (postgres.GetLastEventStaffFieldChangeRow, error)
}

// ParticipantAnswers is one participant's newest registration answers; Values
// is nil for a participant who never answered.
type ParticipantAnswers struct {
	UserID uuid.UUID
	Values map[string]any
}

// ListParticipantAnswers returns every participant of the event with the
// newest registration answers.
func (r *Repository) ListParticipantAnswers(ctx context.Context, eventID uuid.UUID) ([]ParticipantAnswers, error) {
	rows, err := r.q.ListEventParticipantRegistrationAnswers(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]ParticipantAnswers, 0, len(rows))
	for _, row := range rows {
		var values map[string]any
		if len(row.Answers) > 0 {
			if err = json.Unmarshal(row.Answers, &values); err != nil {
				return nil, err
			}
		}
		out = append(out, ParticipantAnswers{UserID: row.UserID, Values: values})
	}
	return out, nil
}

// SetFieldsMissing stores the recounted missing-field numbers by user.
func (r *Repository) SetFieldsMissing(ctx context.Context, eventID uuid.UUID, missing map[uuid.UUID]int32) error {
	if len(missing) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(missing))
	counts := make([]int32, 0, len(missing))
	for id, count := range missing {
		ids = append(ids, id)
		counts = append(counts, count)
	}
	return r.q.SetEventParticipantsFieldsMissing(ctx, postgres.SetEventParticipantsFieldsMissingParams{EventID: eventID, UserIds: ids, Missing: counts})
}

// FieldsMissing is how many required fields the participant has not filled.
func (r *Repository) FieldsMissing(ctx context.Context, eventID, userID uuid.UUID) (int32, error) {
	return r.q.GetEventParticipantFieldsMissing(ctx, postgres.GetEventParticipantFieldsMissingParams{EventID: eventID, UserID: userID})
}

// CountAnswered is the number of participants with a registration answer.
func (r *Repository) CountAnswered(ctx context.Context, eventID uuid.UUID) (int64, error) {
	return r.q.CountEventRegistrationAnswers(ctx, eventID)
}

// LatestAnswerRow is the participant's newest registration answer with the
// version it belongs to.
func (r *Repository) LatestAnswerRow(ctx context.Context, eventID, userID uuid.UUID) (Answer, error) {
	row, err := r.q.GetLatestEventRegistrationAnswerRow(ctx, postgres.GetLatestEventRegistrationAnswerRowParams{EventID: eventID, UserID: userID})
	if err != nil {
		return Answer{}, err
	}
	var values map[string]any
	if err = json.Unmarshal(row.Answers, &values); err != nil {
		return Answer{}, err
	}
	return Answer{EventID: eventID, UserID: userID, FormVersionID: row.FormVersionID, Values: values}, nil
}

// UpdateAnswerValues replaces the values of one answer row and keeps its
// submission time.
func (r *Repository) UpdateAnswerValues(ctx context.Context, eventID, userID, formVersionID uuid.UUID, values map[string]any) (bool, error) {
	encoded, err := json.Marshal(values)
	if err != nil {
		return false, err
	}
	affected, err := r.q.UpdateEventFormAnswerValues(ctx, postgres.UpdateEventFormAnswerValuesParams{EventID: eventID, UserID: userID, FormVersionID: formVersionID, Answers: encoded})
	return affected > 0, err
}

// StaffChange is one recorded edit of staff-only fields.
type StaffChange struct {
	Keys      []string
	ActorID   *uuid.UUID
	ActorName string
	At        time.Time
}

// RecordStaffChange audits who changed which staff-only fields of a
// participant (scope "participant") or a team (scope "team").
func (r *Repository) RecordStaffChange(ctx context.Context, eventID uuid.UUID, scope string, subjectID, actorID uuid.UUID, keys []string, at time.Time) error {
	return r.q.InsertEventStaffFieldChange(ctx, postgres.InsertEventStaffFieldChangeParams{
		ID: uuid.Must(uuid.NewV7()), EventID: eventID, Scope: scope, SubjectID: subjectID,
		ActorID: uuid.NullUUID{UUID: actorID, Valid: actorID != uuid.Nil}, FieldKeys: keys, ChangedAt: at,
	})
}

// LastStaffChange is the newest audit entry of the subject; ok is false when
// the staff-only fields were never edited.
func (r *Repository) LastStaffChange(ctx context.Context, eventID uuid.UUID, scope string, subjectID uuid.UUID) (StaffChange, bool, error) {
	row, err := r.q.GetLastEventStaffFieldChange(ctx, postgres.GetLastEventStaffFieldChangeParams{EventID: eventID, Scope: scope, SubjectID: subjectID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return StaffChange{}, false, nil
		}
		return StaffChange{}, false, err
	}
	change := StaffChange{Keys: row.FieldKeys, ActorName: row.ActorName, At: row.ChangedAt}
	if row.ActorID.Valid {
		id := row.ActorID.UUID
		change.ActorID = &id
	}
	return change, true, nil
}
