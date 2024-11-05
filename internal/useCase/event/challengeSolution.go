package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"time"
)

type (
	IChallengeSolutionService interface {
		SolveChallenge(ctx context.Context, eventID, teamID, userID, challengeID uuid.UUID, solutionAttempt string) (bool, error)

		GetChallengeSolutionAttempts(ctx context.Context, eventID uuid.UUID) ([]*model.TeamChallengeSolutionAttempt, error)
		UpdateChallengeSolutionAttempt(ctx context.Context, solutionAttempt model.TeamChallengeSolutionAttempt) error
	}
)

func (u *EventUseCase) SolveChallenge(ctx context.Context, eventID, challengeID uuid.UUID, solution string) (bool, error) {
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
		return false, model.ErrEventTeamChallengeSolutionAttemptNotAllowed.Cause()
	}

	//get user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return false, model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	solved, err := u.service.SolveChallenge(ctx, eventID, team.ID, userID, challengeID, solution)
	if err != nil {
		return false, err
	}

	return solved, nil
}

func (u *EventUseCase) GetEventChallengeSolutionAttempts(ctx context.Context, eventID uuid.UUID) ([]*model.TeamChallengeSolutionAttempt, error) {
	solutions, err := u.service.GetChallengeSolutionAttempts(ctx, eventID)
	if err != nil {
		return nil, err
	}

	return solutions, nil
}

func (u *EventUseCase) UpdateEventChallengeSolutionAttempt(ctx context.Context, solutionAttempt model.TeamChallengeSolutionAttempt) error {
	err := u.service.UpdateChallengeSolutionAttempt(ctx, solutionAttempt)
	if err != nil {
		return err
	}

	return nil
}
