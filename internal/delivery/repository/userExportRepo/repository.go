// Package userExportRepo reads, for one person, the rows of the «download my data» export. Every query is keyed on
// that person's user id.
package userExportRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	ExportUserParticipations(
		ctx context.Context,
		userID uuid.UUID,
	) ([]postgres.ExportUserParticipationsRow, error)
	ExportUserEventStaffRoles(
		ctx context.Context,
		userID uuid.UUID,
	) ([]postgres.ExportUserEventStaffRolesRow, error)
	ExportUserFlagAttempts(
		ctx context.Context,
		userID uuid.UUID,
	) ([]postgres.ExportUserFlagAttemptsRow, error)
	ExportUserFormAnswers(
		ctx context.Context,
		userID uuid.UUID,
	) ([]postgres.ExportUserFormAnswersRow, error)
	ExportUserInbox(ctx context.Context, userID uuid.UUID) ([]postgres.ExportUserInboxRow, error)
	ExportUserTablePrefs(
		ctx context.Context,
		userID uuid.UUID,
	) ([]postgres.ExportUserTablePrefsRow, error)
	ExportUserNotificationSettings(
		ctx context.Context,
		userID uuid.UUID,
	) ([]postgres.ExportUserNotificationSettingsRow, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func optTime(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

// Fill adds the event-related parts of the export for userID to out.
func (r *Repository) Fill(ctx context.Context, userID uuid.UUID, out *userModel.DataExport) error {
	parts, err := r.q.ExportUserParticipations(ctx, userID)
	if err != nil {
		return err
	}
	out.Participations = make([]userModel.ExportParticipation, 0, len(parts))
	for _, p := range parts {
		item := userModel.ExportParticipation{
			EventID:         p.EventID,
			EventName:       p.EventName,
			EventTag:        p.EventTag,
			Status:          p.Status,
			TeamName:        p.TeamName,
			IsCaptain:       p.IsCaptain,
			Pseudonym:       p.Pseudonym,
			Invited:         p.Invited,
			InvitedToTeam:   p.InvitedToTeam,
			InvitedTeamName: p.InvitedTeamName,
			InvitationSent: optTime(
				p.InvitationSentAt,
			),
			JoinedAt:  p.CreatedAt,
			DecidedAt: optTime(p.DecidedAt),
		}
		if p.TeamRole.Valid {
			role := p.TeamRole.Int16
			item.TeamRole = &role
		}
		out.Participations = append(out.Participations, item)
	}

	return r.fillActivity(ctx, userID, out)
}

// fillActivity adds the staff roles, submissions, form answers, inbox and notification settings of userID.
func (r *Repository) fillActivity(
	ctx context.Context,
	userID uuid.UUID,
	out *userModel.DataExport,
) error {
	roles, err := r.q.ExportUserEventStaffRoles(ctx, userID)
	if err != nil {
		return err
	}
	out.EventStaffRoles = make([]userModel.ExportEventRole, 0, len(roles))
	for _, v := range roles {
		out.EventStaffRoles = append(out.EventStaffRoles, userModel.ExportEventRole{
			EventID: v.EventID, EventName: v.EventName, Role: v.Role, GrantedAt: v.CreatedAt,
		})
	}

	attempts, err := r.q.ExportUserFlagAttempts(ctx, userID)
	if err != nil {
		return err
	}
	out.FlagSubmissions = make([]userModel.ExportFlagSubmission, 0, len(attempts))
	for _, v := range attempts {
		out.FlagSubmissions = append(out.FlagSubmissions, userModel.ExportFlagSubmission{
			EventID:       v.EventID,
			EventName:     v.EventName,
			TeamName:      v.TeamName,
			ChallengeName: v.ChallengeName,
			Answer:        v.Answer,
			Correct:       v.Correct,
			Practice:      v.Practice,
			ReceivedAt:    v.ReceivedAt,
		})
	}

	forms, err := r.q.ExportUserFormAnswers(ctx, userID)
	if err != nil {
		return err
	}
	out.FormAnswers = make([]userModel.ExportFormAnswer, 0, len(forms))
	for _, v := range forms {
		answers := json.RawMessage(v.Answers)
		if len(answers) == 0 {
			answers = json.RawMessage("null")
		}
		out.FormAnswers = append(out.FormAnswers, userModel.ExportFormAnswer{
			EventID:       v.EventID,
			EventName:     v.EventName,
			FormVersionID: v.FormVersionID,
			Answers:       answers,
			SubmittedAt:   v.SubmittedAt,
		})
	}

	inbox, err := r.q.ExportUserInbox(ctx, userID)
	if err != nil {
		return err
	}
	out.Inbox = make([]userModel.ExportInboxItem, 0, len(inbox))
	for _, v := range inbox {
		out.Inbox = append(out.Inbox, userModel.ExportInboxItem{
			Title:     v.Title,
			Body:      v.Body,
			Link:      v.Link,
			Type:      v.NotificationType,
			Category:  v.Category,
			EventName: v.EventName,
			CreatedAt: v.CreatedAt,
			ReadAt:    optTime(v.ReadAt),
		})
	}

	settings, err := r.q.ExportUserNotificationSettings(ctx, userID)
	if err != nil {
		return err
	}
	out.NotificationSettings = make([]userModel.ExportNotificationSetting, 0, len(settings))
	for _, v := range settings {
		out.NotificationSettings = append(
			out.NotificationSettings,
			userModel.ExportNotificationSetting{
				Type: v.NotificationType, Channel: v.Channel, Enabled: v.Enabled,
			},
		)
	}
	views, err := r.q.ExportUserTablePrefs(ctx, userID)
	if err != nil {
		return err
	}
	out.TableViews = make([]userModel.ExportTableView, 0, len(views))
	for _, v := range views {
		out.TableViews = append(
			out.TableViews,
			userModel.ExportTableView{
				Table:     v.TableKey,
				Prefs:     json.RawMessage(v.Prefs),
				UpdatedAt: v.UpdatedAt,
			},
		)
	}
	return nil
}
