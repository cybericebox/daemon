package eventAnalyticsRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

// ParticipantQueries are the statements of «Учасники» and «Комунікації»
// (§6.2, §6.7); Queries embeds it.
type ParticipantQueries interface {
	GetEventParticipantFunnel(context.Context, uuid.UUID) (postgres.GetEventParticipantFunnelRow, error)
	ListEventRegistrationDays(context.Context, postgres.ListEventRegistrationDaysParams) ([]postgres.ListEventRegistrationDaysRow, error)
	ListEventTeamFill(context.Context, uuid.UUID) ([]postgres.ListEventTeamFillRow, error)
	ListEventRegistrationFormVersions(context.Context, uuid.UUID) ([]postgres.ListEventRegistrationFormVersionsRow, error)
	ListEventRegistrationAnswers(context.Context, uuid.UUID) ([]postgres.ListEventRegistrationAnswersRow, error)
	ListEventDropOffParticipants(context.Context, postgres.ListEventDropOffParticipantsParams) ([]postgres.ListEventDropOffParticipantsRow, error)
	ListEventNotificationDispatchStats(context.Context, postgres.ListEventNotificationDispatchStatsParams) ([]postgres.ListEventNotificationDispatchStatsRow, error)
	ListEventInAppStats(context.Context, postgres.ListEventInAppStatsParams) ([]postgres.ListEventInAppStatsRow, error)
	ListEventFormCompletion(context.Context, postgres.ListEventFormCompletionParams) ([]postgres.ListEventFormCompletionRow, error)
	GetMailFunnels(context.Context, postgres.GetMailFunnelsParams) (postgres.GetMailFunnelsRow, error)
}

type (
	// ParticipantFunnel counts participants at each stage of participation.
	ParticipantFunnel struct {
		Invited, Registered, Approved, InTeam, Attempted, Solved int64
	}

	// RegistrationDay is the registrations of one UTC day and channel
	// (open, approval, invitation).
	RegistrationDay struct {
		Day           time.Time
		Channel       string
		Registrations int64
	}

	// TeamFill is one team with its size and the invitees not joined yet.
	TeamFill struct {
		ID              uuid.UUID
		Name            string
		Members         int32
		Individual      bool
		Admitted        bool
		PendingInvitees int64
	}

	// FormVersion is a version of the registration form: its questions.
	FormVersion struct {
		ID      uuid.UUID
		Version int32
		Blocks  []eventContentModel.Block
	}

	// FormAnswer is one participant's latest registration answers.
	FormAnswer struct {
		VersionID uuid.UUID
		Answers   map[string]any
	}

	// DropOff is an approved participant who never submitted an attempt.
	DropOff struct {
		UserID                uuid.UUID
		Name, Email, TeamName string
		RegisteredAt          time.Time
		ApprovedAt            *time.Time
		OpenedTasks           int64
	}

	// DispatchCount is the number of dispatch targets of one notification type
	// on one channel in one status.
	DispatchCount struct {
		Type, Channel, Status string
		Targets               int64
	}

	// InAppCount is the in-app notifications of one type and how many were read.
	InAppCount struct {
		Type        string
		Total, Read int64
	}

	// FormCompletion is the delivery figures of one event form.
	FormCompletion struct {
		ID                           uuid.UUID
		Title, Purpose               string
		Enabled                      bool
		Assigned, Completed, Answers int64
	}
)

// ParticipantFunnel reads the participation funnel.
func (r *Repository) ParticipantFunnel(ctx context.Context, eventID uuid.UUID) (ParticipantFunnel, error) {
	row, err := r.q.GetEventParticipantFunnel(ctx, eventID)
	if err != nil {
		return ParticipantFunnel{}, err
	}
	return ParticipantFunnel{
		Invited: row.Invited, Registered: row.Registered, Approved: row.Approved,
		InTeam: row.InTeam, Attempted: row.Attempted, Solved: row.Solved,
	}, nil
}

// RegistrationDays reads registrations per UTC day and channel; nil bounds
// leave the window open.
func (r *Repository) RegistrationDays(ctx context.Context, eventID uuid.UUID, from, to *time.Time) ([]RegistrationDay, error) {
	rows, err := r.q.ListEventRegistrationDays(ctx, postgres.ListEventRegistrationDaysParams{EventID: eventID, FromAt: timestamptz(from), ToAt: timestamptz(to)})
	if err != nil {
		return nil, err
	}
	out := make([]RegistrationDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, RegistrationDay{Day: row.Day, Channel: row.Channel, Registrations: row.Registrations})
	}
	return out, nil
}

// TeamFill reads every team (outside the moderators team) with its fill.
func (r *Repository) TeamFill(ctx context.Context, eventID uuid.UUID) ([]TeamFill, error) {
	rows, err := r.q.ListEventTeamFill(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]TeamFill, 0, len(rows))
	for _, row := range rows {
		out = append(out, TeamFill{
			ID: row.ID, Name: row.Name, Members: row.MemberCount, Individual: row.Individual,
			Admitted: row.Admitted, PendingInvitees: row.PendingInvitees,
		})
	}
	return out, nil
}

// RegistrationFormVersions reads the versions of the registration form,
// oldest first. A version whose document cannot be read is skipped: reads
// forgive legacy data.
func (r *Repository) RegistrationFormVersions(ctx context.Context, eventID uuid.UUID) ([]FormVersion, error) {
	rows, err := r.q.ListEventRegistrationFormVersions(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]FormVersion, 0, len(rows))
	for _, row := range rows {
		var doc eventContentModel.Document
		if json.Unmarshal(row.Document, &doc) != nil {
			continue
		}
		out = append(out, FormVersion{ID: row.ID, Version: row.Version, Blocks: doc.Blocks})
	}
	return out, nil
}

// RegistrationAnswers reads each participant's latest registration answers.
func (r *Repository) RegistrationAnswers(ctx context.Context, eventID uuid.UUID) ([]FormAnswer, error) {
	rows, err := r.q.ListEventRegistrationAnswers(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]FormAnswer, 0, len(rows))
	for _, row := range rows {
		var answers map[string]any
		if json.Unmarshal(row.Answers, &answers) != nil {
			continue
		}
		out = append(out, FormAnswer{VersionID: row.FormVersionID, Answers: answers})
	}
	return out, nil
}

// DropOffs reads up to limit approved participants without an attempt and the
// total number of them.
func (r *Repository) DropOffs(ctx context.Context, eventID uuid.UUID, limit int32) ([]DropOff, int64, error) {
	rows, err := r.q.ListEventDropOffParticipants(ctx, postgres.ListEventDropOffParticipantsParams{EventID: eventID, RowLimit: limit})
	if err != nil {
		return nil, 0, err
	}
	var total int64
	out := make([]DropOff, 0, len(rows))
	for _, row := range rows {
		total = row.Total
		name := row.FirstName
		if row.LastName != "" {
			name += " " + row.LastName
		}
		if name == "" {
			name = row.DisplayName
		}
		out = append(out, DropOff{
			UserID: row.UserID, Name: name, Email: row.Email, TeamName: row.TeamName,
			RegisteredAt: row.RegisteredAt, ApprovedAt: timePtr(row.DecidedAt), OpenedTasks: row.OpenedTasks,
		})
	}
	return out, total, nil
}

// DispatchStats counts the event's notification dispatch targets in the
// window; nil bounds leave it open.
func (r *Repository) DispatchStats(ctx context.Context, eventID uuid.UUID, from, to *time.Time) ([]DispatchCount, error) {
	rows, err := r.q.ListEventNotificationDispatchStats(ctx, postgres.ListEventNotificationDispatchStatsParams{
		EventID: uuid.NullUUID{UUID: eventID, Valid: true}, FromAt: timestamptz(from), ToAt: timestamptz(to),
	})
	if err != nil {
		return nil, err
	}
	out := make([]DispatchCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, DispatchCount{Type: row.NotificationType, Channel: row.Channel, Status: row.Status, Targets: row.Targets})
	}
	return out, nil
}

// InAppStats counts the event's in-app notifications and reads per type.
func (r *Repository) InAppStats(ctx context.Context, eventID uuid.UUID, from, to *time.Time) ([]InAppCount, error) {
	rows, err := r.q.ListEventInAppStats(ctx, postgres.ListEventInAppStatsParams{
		EventID: uuid.NullUUID{UUID: eventID, Valid: true}, FromAt: timestamptz(from), ToAt: timestamptz(to),
	})
	if err != nil {
		return nil, err
	}
	out := make([]InAppCount, 0, len(rows))
	for _, row := range rows {
		out = append(out, InAppCount{Type: row.NotificationType, Total: row.Total, Read: row.Read})
	}
	return out, nil
}

// FormCompletion reads the delivery figures of every event form.
func (r *Repository) FormCompletion(ctx context.Context, eventID uuid.UUID, from, to *time.Time) ([]FormCompletion, error) {
	rows, err := r.q.ListEventFormCompletion(ctx, postgres.ListEventFormCompletionParams{EventID: eventID, FromAt: timestamptz(from), ToAt: timestamptz(to)})
	if err != nil {
		return nil, err
	}
	out := make([]FormCompletion, 0, len(rows))
	for _, row := range rows {
		out = append(out, FormCompletion{
			ID: row.ID, Title: row.Title, Purpose: row.Purpose, Enabled: row.Enabled,
			Assigned: row.Assigned, Completed: row.Completed, Answers: row.Answers,
		})
	}
	return out, nil
}

// MailFunnels reads the invitation, registration and application outcomes of
// one event (moderators and hidden teams left out).
func (r *Repository) MailFunnels(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (dispatchModel.MailFunnels, error) {
	row, err := r.q.GetMailFunnels(ctx, postgres.GetMailFunnelsParams{
		EventID: uuid.NullUUID{UUID: eventID, Valid: true}, FromAt: timestamptz(from), ToAt: timestamptz(to),
	})
	if err != nil {
		return dispatchModel.MailFunnels{}, err
	}
	return dispatchRepo.MailFunnelsFromRow(row), nil
}
