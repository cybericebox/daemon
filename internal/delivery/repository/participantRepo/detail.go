package participantRepo

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// Detailed is one participant for the manager's detail view: the list row
// plus the participant's own attempt figures.
type Detailed struct {
	Listed
	Attempts int64
	Solves   int64
}

// Detail loads one participant of an event. Not found propagates raw for the
// caller to classify.
func (r *Repository) Detail(ctx context.Context, eventID, userID uuid.UUID) (Detailed, error) {
	row, err := r.q.GetEventParticipantDetail(ctx, postgres.GetEventParticipantDetailParams{EventID: eventID, UserID: userID})
	if err != nil {
		return Detailed{}, err
	}
	participant := ToDomain(postgres.EventParticipant{
		EventID: row.EventID, UserID: row.UserID, Status: row.Status, CreatedAt: row.CreatedAt, DecidedAt: row.DecidedAt,
		DecidedBy: row.DecidedBy, TeamID: row.TeamID, TeamRole: row.TeamRole, InvitedBy: row.InvitedBy, Invited: row.Invited,
		InvitedTeamID: row.InvitedTeamID, InvitedToTeam: row.InvitedToTeam, Pseudonym: row.Pseudonym,
		InvitationSentAt: row.InvitationSentAt, FieldsMissing: row.FieldsMissing,
	})
	return Detailed{
		Listed: Listed{
			Participant: participant, FirstName: row.FirstName, LastName: row.LastName, Email: row.Email,
			DisplayName: row.DisplayName, TeamName: row.TeamName, TeamHidden: row.TeamHidden,
			InvitedTeamName: row.InvitedTeamName, FieldsMissing: row.FieldsMissing,
			LastSeenAt: timePtr(row.LastSeenAt), LastLabAt: labAtPtr(row.LastLabAt),
		},
		Attempts: row.Attempts, Solves: row.Solves,
	}, nil
}
