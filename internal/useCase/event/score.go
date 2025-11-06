package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	IScoreService interface {
		GetEventScore(
			ctx context.Context,
			eventID uuid.UUID,
			fromTime, toTime time.Time,
		) (*eventModel.EventScore, error)
	}
)

func (u *EventUseCase) GetScore(ctx context.Context, eventID uuid.UUID) (*eventModel.EventScore, error) {
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event by id").Err()
	}

	startTime := event.StartTime
	finalTime := time.Now().UTC()
	if finalTime.After(event.FinishTime) {
		finalTime = event.FinishTime
	}

	// if event has not started yet, anyone can't see the scoreboard
	if event.StartTime.After(time.Now().UTC()) {
		return nil, eventModel.ErrEventScoreNotAvailable.Err()
	}

	// if scoreboard is public, then return the scoreboard
	if event.ScoreboardAvailability == eventModel.PublicScoreboardAvailabilityType {
		score, err := u.service.GetEventScore(ctx, eventID, startTime, finalTime)
		if err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get score").Err()
		}
		return score, nil
	}

	// return private scoreboard only if the user is a participant
	if event.ScoreboardAvailability == eventModel.PrivateScoreboardAvailabilityType {

		joinedStatus, err := u.GetSelfJoinEventStatus(ctx, eventID)
		if err != nil || joinedStatus != eventModel.ApprovedParticipationStatus {
			return nil, eventModel.ErrEventScoreNotAvailable.Err()
		}

		score, err := u.service.GetEventScore(ctx, eventID, startTime, finalTime)
		if err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get score").Err()
		}
		return score, nil
	}

	// return hidden scoreboard only if the user is an administrator
	if event.ScoreboardAvailability == eventModel.HiddenScoreboardAvailabilityType {
		userRole, err := tools.GetCurrentUserRoleFromContext(ctx)
		if err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user role from context").Err()
		}
		if userRole == userModel.AdministratorRole {
			score, err := u.service.GetEventScore(ctx, eventID, startTime, finalTime)
			if err != nil {
				return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get score").Err()
			}
			return score, nil
		}
	}
	return nil, eventModel.ErrEventScoreNotAvailable.Err()
}

func (u *EventUseCase) ProtectScore(ctx context.Context, eventID uuid.UUID) (bool, error) {
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return true, model.ErrPlatform.WithError(err).WithMessage("Failed to get event by id").Err()
	}

	// if event scoreboard is public, then return true
	if event.ScoreboardAvailability == eventModel.PublicScoreboardAvailabilityType {
		return false, nil
	}

	// protect by default
	return true, nil
}
