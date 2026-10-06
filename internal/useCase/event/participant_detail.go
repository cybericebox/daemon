package event

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// How a participant came into the event.
const (
	JoinedOpen       = "open"     // registered and was approved on the spot
	JoinedApproval   = "approval" // registered and went through a decision
	JoinedInvitation = "invitation"
)

// ParticipantDetailView is one participant with what the manager's detail
// view adds to the list row.
type ParticipantDetailView struct {
	ParticipantView
	// TeamRole is the role in the team (captain or member); nil without a team.
	TeamRole  *participantModel.TeamRole
	JoinedVia string
	// Attempts submitted by the participant, and the distinct tasks they solved.
	Attempts int64
	Solves   int64
}

// GetParticipantDetail reads one participant of the event for the manager.
func (u *EventUseCase) GetParticipantDetail(ctx context.Context, eventID, userID uuid.UUID) (ParticipantDetailView, error) {
	row, err := u.participants.Detail(ctx, eventID, userID)
	if repositoryTools.IsObjectNotFoundError(err) || errors.Is(err, pgx.ErrNoRows) {
		return ParticipantDetailView{}, participantModel.ErrParticipantNotFound.Err()
	}
	if err != nil {
		return ParticipantDetailView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
	}
	views, err := u.participantViews(ctx, eventID, []participantRepo.Listed{row.Listed})
	if err != nil {
		return ParticipantDetailView{}, err
	}
	p := row.Participant
	via := JoinedApproval
	switch {
	case p.Invited:
		via = JoinedInvitation
	case p.Status == participantModel.StatusApproved && !p.DecidedBy.Valid:
		via = JoinedOpen
	}
	return ParticipantDetailView{ParticipantView: views[0], TeamRole: p.TeamRole, JoinedVia: via, Attempts: row.Attempts, Solves: row.Solves}, nil
}
