package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	IChallengeSolutionService interface {
		SolveEventChallenge(
			ctx context.Context,
			teamID, userID, challengeID uuid.UUID,
			solutionAttempt string,
			timestamp time.Time,
		) (bool, error)

		GetEventChallengeSolutionAttempts(
			ctx context.Context,
			eventID uuid.UUID,
			page, pageSize int,
		) ([]*eventModel.TeamChallengeSolutionAttempt, error)
		UpdateEventChallengeSolutionAttempt(
			ctx context.Context,
			solutionAttempt eventModel.TeamChallengeSolutionAttempt,
		) error
	}
)

func (u *EventUseCase) SolveChallenge(ctx context.Context, eventID, challengeID uuid.UUID, solution string) (
	bool,
	error,
) {
	// check if user has team in event
	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return false, err
	}

	// check if allowed to solve challenge
	// if event is not started, or ended, or paused
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return false, err
	}

	if event.StartTime.After(time.Now().UTC()) || event.FinishTime.Before(time.Now().UTC()) {
		return false, eventModel.ErrEventTeamChallengeSolutionAttemptNotAllowed.Err()
	}

	// get user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	solved, err := u.service.SolveEventChallenge(ctx, team.ID, userID, challengeID, solution, time.Now().UTC())
	if err != nil {
		return false, err
	}

	return solved, nil
}

func (u *EventUseCase) GetEventChallengeSolutionAttempts(
	ctx context.Context,
	eventID uuid.UUID,
	page, pageSize int,
) ([]*eventModel.TeamChallengeSolutionAttempt, error) {
	solutions, err := u.service.GetEventChallengeSolutionAttempts(ctx, eventID, page, pageSize)
	if err != nil {
		return nil, err
	}

	return solutions, nil
}

func (u *EventUseCase) UpdateEventChallengeSolutionAttempt(
	ctx context.Context,
	solutionAttempt eventModel.TeamChallengeSolutionAttempt,
) error {
	err := u.service.UpdateEventChallengeSolutionAttempt(ctx, solutionAttempt)
	if err != nil {
		return err
	}

	return nil
}
