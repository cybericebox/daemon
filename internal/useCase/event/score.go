package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"time"
)

type (
	IScoreService interface {
		GetScore(ctx context.Context, eventID uuid.UUID) (*model.EventScore, error)
	}
)

func (u *EventUseCase) GetScore(ctx context.Context, eventID uuid.UUID) (*model.EventScore, error) {
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, err
	}

	// if event has not started yet, anyone can't see the scoreboard
	if event.StartTime.After(time.Now().UTC()) {
		return nil, model.ErrScoreNotAvailable
	}

	// if scoreboard is public, then return the scoreboard
	if event.ScoreboardAvailability == model.PublicScoreboardAvailabilityType {
		return u.service.GetScore(ctx, eventID)
	}

	// return private scoreboard only if the user is a participant
	if event.ScoreboardAvailability == model.PrivateScoreboardAvailabilityType {
		if _, err := u.GetSelfTeam(ctx, eventID); err != nil {
			return nil, model.ErrScoreNotAvailable
		}
		return u.service.GetScore(ctx, eventID)
	}

	// return hidden scoreboard only if the user is an administrator
	if event.ScoreboardAvailability == model.HiddenScoreboardAvailabilityType {
		userRole, err := tools.GetCurrentUserRoleFromContext(ctx)
		if err != nil {
			return nil, err
		}
		if userRole == model.AdministratorRole {
			return u.service.GetScore(ctx, eventID)
		}
	}
	return nil, model.ErrScoreNotAvailable
}

func (u *EventUseCase) ProtectScore(ctx context.Context, eventID uuid.UUID) (bool, error) {
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return true, err
	}

	// if event scoreboard is public, then return true
	if event.ScoreboardAvailability == model.PublicScoreboardAvailabilityType {
		return false, nil
	}

	// protect by default
	return true, nil
}
