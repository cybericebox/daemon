package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	challengeAttemptRepo "github.com/cybericebox/daemon/internal/delivery/repository/challengeAttemptRepo"
	teamChallengeRepo "github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	"github.com/cybericebox/daemon/pkg/pagination"
)

func (u *EventUseCase) ListSolutionAttempts(ctx context.Context, f ListSolutionAttemptsFilter) (SolutionAttemptsListResult, error) {
	limit := f.PageSize
	if limit <= 0 || limit > pagination.MaxPageSize {
		limit = pagination.DefaultPageSize
	}
	cursorAt, cursorID := cursorSentinelTime, maxUUID
	if f.Cursor != uuid.Nil {
		at, id, err := u.attempts.Cursor(ctx, f.EventID, f.Cursor)
		if err != nil {
			// An unknown cursor must not silently restart at page 1: the
			// client would show duplicates as if it were the next page.
			if repositoryTools.IsObjectNotFoundError(err) {
				return SolutionAttemptsListResult{}, challengeAttemptModel.ErrAttemptCursorInvalid.Err()
			}
			return SolutionAttemptsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to resolve solution attempt cursor").Err()
		}
		cursorAt, cursorID = at, id
	}
	filter := challengeAttemptRepo.ListFilter{EventID: f.EventID, TeamID: f.TeamID, ParticipantID: f.ParticipantID, ChallengeID: f.ChallengeID, Correct: f.Correct, FromAt: f.FromAt, ToAt: f.ToAt, CursorReceivedAt: cursorAt, CursorID: cursorID, Limit: int32(limit + 1)}
	rows, err := u.attempts.List(ctx, filter)
	if err != nil {
		return SolutionAttemptsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list solution attempts").Err()
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]SolutionAttemptView, 0, len(rows))
	for _, row := range rows {
		items = append(items, SolutionAttemptView{ID: row.ID, EventTeamID: row.EventTeamID, TeamName: row.TeamName, TeamChallengeID: row.TeamChallengeID, EventChallengeID: row.EventChallengeID, ChallengeName: row.ChallengeName, EventExerciseID: row.EventExerciseID, UserID: row.UserID, ParticipantName: row.ParticipantName, Answer: row.Answer, ExpectedFlag: row.ExpectedFlag, AutomaticCorrect: row.AutomaticCorrect, Decision: row.Decision, DecisionReason: row.DecisionReason, DecidedBy: row.DecidedBy, DecidedAt: row.DecidedAt, Correct: row.Correct, ReceivedAt: row.ReceivedAt, Points: row.Points})
	}
	total, err := u.attempts.Count(ctx, filter)
	if err != nil {
		return SolutionAttemptsListResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count solution attempts").Err()
	}
	result := SolutionAttemptsListResult{Items: items, HasMore: hasMore, Total: total}
	if hasMore {
		result.NextCursor = items[len(items)-1].ID
	}
	return result, nil
}

// DecideSolutionAttempt appends an auditable moderator verdict without
// modifying the submitted answer. Refreshing the rebuildable solved-task
// projection in the same transaction keeps fast result reads aligned with the
// latest effective verdict while preserving the attempt's original received_at.
func (u *EventUseCase) DecideSolutionAttempt(ctx context.Context, eventID, attemptID uuid.UUID, in DecideSolutionAttemptInput) (SolutionAttemptDecisionView, error) {
	if u.uow == nil {
		return SolutionAttemptDecisionView{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	decision, err := challengeAttemptModel.NewManualDecision(attemptID, in.Decision, in.Reason, in.DecidedBy, time.Now())
	if err != nil {
		return SolutionAttemptDecisionView{}, err
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return SolutionAttemptDecisionView{}, err
	}
	defer unit.Restore()
	attempts := challengeAttemptRepo.New(txRepo)
	target, err := attempts.GetForDecision(txCtx, eventID, attemptID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return SolutionAttemptDecisionView{}, challengeAttemptModel.ErrAttemptNotFound.Err()
		}
		return SolutionAttemptDecisionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get challenge attempt").Err()
	}
	beforeChallenge, err := teamChallengeRepo.New(txRepo).Get(txCtx, target.EventTeamID, target.EventChallengeID)
	if err != nil {
		return SolutionAttemptDecisionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get current challenge result").Err()
	}
	if err = attempts.RecordDecision(txCtx, decision); err != nil {
		return SolutionAttemptDecisionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record challenge attempt decision").Err()
	}
	effective, err := attempts.EffectiveSolvedAt(txCtx, target.TeamChallengeID)
	if err != nil {
		return SolutionAttemptDecisionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to calculate challenge result").Err()
	}
	var award *int32
	if effective.Solved && beforeChallenge.SolvedAt == nil {
		award, err = fixedAward(txCtx, attempts, target.TeamChallengeID, effective.SolvedAt)
		if err != nil {
			return SolutionAttemptDecisionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to calculate challenge award").Err()
		}
	}
	afterAt, afterSolved, err := attempts.RefreshSolvedProjectionValue(txCtx, target.TeamChallengeID, effective, award)
	if err != nil {
		return SolutionAttemptDecisionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to recalculate challenge result").Err()
	}
	var after *time.Time
	if afterSolved {
		after = &afterAt
	}
	if award == nil && afterSolved && beforeChallenge.SolvedAt == nil {
		err = recordScoreboardRecalculation(txCtx, txRepo, eventID)
	} else {
		err = recordResultChange(txCtx, txRepo, eventID, target.EventTeamID, target.EventChallengeID, beforeChallenge.SolvedAt, after)
	}
	if err != nil {
		return SolutionAttemptDecisionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
	}
	if afterSolved != (beforeChallenge.SolvedAt != nil) {
		if err = u.requestLabAccessSyncInTransaction(txCtx, txRepo, target.EventTeamID, time.Now()); err != nil {
			return SolutionAttemptDecisionView{}, err
		}
	}
	if err = unit.Save(); err != nil {
		return SolutionAttemptDecisionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to decide challenge attempt").Err()
	}
	return SolutionAttemptDecisionView{AttemptID: attemptID, Decision: decision.Decision, Reason: decision.Reason, DecidedBy: decision.DecidedBy, DecidedAt: decision.DecidedAt, Correct: decision.Decision.EffectiveCorrect(target.AutomaticCorrect)}, nil
}
