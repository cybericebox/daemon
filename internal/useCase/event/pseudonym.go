package event

import (
	"context"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

// ParticipantNameView is the caller's own identity in an event.
type ParticipantNameView struct {
	RealName    string
	Pseudonym   *string
	DisplayName string
}

// SetOwnPseudonym sets or clears the caller's per-event pseudonym. It works
// in both participation modes, only while the organizer allows pseudonyms
// (clearing is always allowed) and only before the event starts. Uniqueness
// (case-insensitive) is the repository's unique index. Route gate: PermSelf.
func (u *EventUseCase) SetOwnPseudonym(ctx context.Context, eventID, userID uuid.UUID, pseudonym *string) (ParticipantNameView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ParticipantNameView{}, participantModel.ErrParticipantNotFound.Err()
		}
		return ParticipantNameView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant").Err()
	}
	config, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return ParticipantNameView{}, err
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ParticipantNameView{}, eventModel.ErrEventNotFound.Err()
		}
		return ParticipantNameView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if err = p.SetPseudonym(pseudonym, config.AllowPseudonyms, e.Lifecycle.HasStarted(time.Now())); err != nil {
		return ParticipantNameView{}, err
	}
	affected, err := u.participants.SetPseudonym(ctx, eventID, userID, p.Pseudonym)
	if err != nil {
		if creator, ok := repositoryTools.UniqueViolationError(err, participantModel.ErrPseudonymTaken); ok {
			return ParticipantNameView{}, creator.Err()
		}
		return ParticipantNameView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save pseudonym").Err()
	}
	if affected == 0 {
		// The participant was read above, so a refused write with a value is a
		// pseudonym that spells another participant's real name.
		if p.Pseudonym != nil {
			if _, getErr := u.participants.Get(ctx, eventID, userID); getErr == nil {
				return ParticipantNameView{}, participantModel.ErrPseudonymTaken.Err()
			}
		}
		return ParticipantNameView{}, participantModel.ErrParticipantNotFound.Err()
	}
	return u.ownParticipantName(ctx, eventID, userID)
}

func (u *EventUseCase) ownParticipantName(ctx context.Context, eventID, userID uuid.UUID) (ParticipantNameView, error) {
	profile, err := u.participants.Profile(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ParticipantNameView{}, participantModel.ErrParticipantNotFound.Err()
		}
		return ParticipantNameView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant profile").Err()
	}
	return ParticipantNameView{
		RealName:    strings.TrimSpace(profile.FirstName + " " + profile.LastName),
		Pseudonym:   profile.Pseudonym,
		DisplayName: profile.DisplayName,
	}, nil
}
