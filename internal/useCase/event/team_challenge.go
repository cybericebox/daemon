package event

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/challengeAttemptRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/requestIdempotencyRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

const challengeSubmissionIdempotencyScope = "event.challenge.submit"

type storedChallengeSubmissionResult struct {
	Correct    bool `json:"correct"`
	FirstSolve bool `json:"firstSolve"`
}

// SubmitChallenge checks an answer; a refused submission (rate limit,
// closed runtime, locked task, ...) is logged for analytics.
func (u *EventUseCase) SubmitChallenge(ctx context.Context, eventID, userID, challengeID uuid.UUID, in SubmitChallengeInput) (SubmitChallengeResult, error) {
	result, err := u.submitChallenge(ctx, eventID, userID, challengeID, in, false)
	if err != nil {
		u.recordRejectedSubmission(ctx, eventID, userID, challengeID, in.ReceivedAt, err)
	}
	return result, err
}

// SubmitModeratorsChallenge submits an answer as the event's moderators team.
// It is a normal submission of a normal (hidden) team: the attempt, solve,
// points and hints are recorded like any team's. The moderators may test
// tasks before the start and outside the competition window, but not once
// the event is withdrawn or archived. The board they see is not gated by
// prerequisites or publication.
func (u *EventUseCase) SubmitModeratorsChallenge(ctx context.Context, eventID, userID, challengeID uuid.UUID, in SubmitChallengeInput) (SubmitChallengeResult, error) {
	return u.submitChallenge(ctx, eventID, userID, challengeID, in, true)
}

// moderatorsSubmitTeam returns the moderators team for a submission and checks
// that the event still runs: the moderators may answer before the start and
// outside the window, but not after it is withdrawn.
func (u *EventUseCase) moderatorsSubmitTeam(ctx context.Context, txRepo eventRepo.Queries, eventID uuid.UUID, at time.Time) (uuid.UUID, error) {
	event, err := eventRepo.New(txRepo).GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return uuid.Nil, eventModel.ErrEventNotFound.Err()
		}
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event lifecycle").Err()
	}
	if event.Lifecycle.Status(at) == eventModel.LifecycleWithdrawn || event.Status(at) == eventModel.EventArchivedStatus {
		return uuid.Nil, eventModel.ErrEventRuntimeNotOpen.Err()
	}
	return u.resolveModeratorsTeam(ctx, event)
}

// requirePrerequisitesSolved is the board's lock: a challenge opens for a team
// only once the team solved every prerequisite of it.
func requirePrerequisitesSolved(ctx context.Context, challenges *eventChallengeRepo.Repository, teamChallenges *teamChallengeRepo.Repository, teamID, eventChallengeID uuid.UUID) error {
	prerequisites, err := challenges.Prerequisites(ctx, eventChallengeID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get challenge prerequisites").Err()
	}
	for _, prerequisiteID := range prerequisites {
		prerequisite, prerequisiteErr := teamChallenges.Get(ctx, teamID, prerequisiteID)
		if prerequisiteErr != nil || prerequisite.SolvedAt == nil {
			return teamChallengeModel.ErrTeamChallengePrerequisites.Err()
		}
	}
	return nil
}

func (u *EventUseCase) submitChallenge(ctx context.Context, eventID, userID, challengeID uuid.UUID, in SubmitChallengeInput, moderators bool) (SubmitChallengeResult, error) {
	if u.uow == nil {
		return SubmitChallengeResult{}, model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	if in.IdempotencyKey == uuid.Nil {
		return SubmitChallengeResult{}, challengeAttempt.ErrIdempotencyKeyRequired.Err()
	}
	canonicalAnswer := strings.TrimSpace(in.Answer)
	requestHash := sha256.Sum256([]byte(eventID.String() + "\x00" + challengeID.String() + "\x00" + canonicalAnswer))
	now := time.Now()
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return SubmitChallengeResult{}, err
	}
	defer unit.Restore()
	idempotency := requestIdempotencyRepo.New(txRepo)
	record, created, err := idempotency.Reserve(txCtx, requestIdempotencyRepo.Key{OwnerID: userID, Scope: challengeSubmissionIdempotencyScope, IdempotencyKey: in.IdempotencyKey}, requestHash[:], now, now.Add(24*time.Hour))
	if err != nil {
		return SubmitChallengeResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to reserve idempotency key").Err()
	}
	if !created {
		if !bytes.Equal(record.RequestHash, requestHash[:]) {
			return SubmitChallengeResult{}, challengeAttempt.ErrIdempotencyKeyConflict.Err()
		}
		if !record.Completed {
			return SubmitChallengeResult{}, challengeAttempt.ErrIdempotencyInProgress.Err()
		}
		var stored storedChallengeSubmissionResult
		if err = json.Unmarshal(record.ResponseBody, &stored); err != nil {
			return SubmitChallengeResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read idempotent response").Err()
		}
		return SubmitChallengeResult{Correct: stored.Correct, FirstSolve: stored.FirstSolve}, nil
	}
	var teamID uuid.UUID
	if moderators {
		if teamID, err = u.moderatorsSubmitTeam(txCtx, txRepo, eventID, in.ReceivedAt); err != nil {
			return SubmitChallengeResult{}, err
		}
	} else {
		p, getErr := participantRepo.New(txRepo).Get(txCtx, eventID, userID)
		if getErr != nil {
			return SubmitChallengeResult{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get event participant").Err()
		}
		if p.Status != participantModel.StatusApproved || p.TeamID == nil {
			return SubmitChallengeResult{}, participantModel.ErrParticipantNotApproved.Err()
		}
		teamID = *p.TeamID
		if err = requireTeamAdmitted(txCtx, eventTeamRepo.New(txRepo), eventID, teamID); err != nil {
			return SubmitChallengeResult{}, err
		}
		if err = requireEventCapability(txCtx, eventFormRepo.New(txRepo), eventID, userID, eventFormModel.CapabilityChallengeSubmit); err != nil {
			return SubmitChallengeResult{}, err
		}
		if err = requireFieldsFilled(txCtx, eventFormRepo.New(txRepo), eventTeamRepo.New(txRepo), eventID, userID, teamID); err != nil {
			return SubmitChallengeResult{}, err
		}
		if err = requireRuntimeOpenAt(txCtx, eventRepo.New(txRepo), eventID, in.ReceivedAt); err != nil {
			return SubmitChallengeResult{}, err
		}
	}
	tc, err := teamChallengeRepo.New(txRepo).Get(txCtx, teamID, challengeID)
	if err != nil {
		return SubmitChallengeResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team challenge").Err()
	}
	if moderators {
		if tc.EventID != eventID || !onModeratorsBoard(tc.Readiness) {
			return SubmitChallengeResult{}, teamChallengeModel.ErrTeamChallengeTransition.Err()
		}
	} else {
		if tc.EventID != eventID || tc.Readiness != teamChallengeModel.ReadinessPublished {
			return SubmitChallengeResult{}, teamChallengeModel.ErrTeamChallengeTransition.Err()
		}
		published, pubErr := eventChallengeRepo.New(txRepo).Published(txCtx, tc.EventChallengeID)
		if pubErr != nil {
			return SubmitChallengeResult{}, model.ErrPlatform.WithError(pubErr).WithMessage("Failed to get event challenge publication").Err()
		}
		if !published {
			return SubmitChallengeResult{}, teamChallengeModel.ErrTeamChallengeTransition.Err()
		}
		if err = requirePrerequisitesSolved(txCtx, eventChallengeRepo.New(txRepo), teamChallengeRepo.New(txRepo), teamID, tc.EventChallengeID); err != nil {
			return SubmitChallengeResult{}, err
		}
	}
	if err = throttleSubmission(txCtx, challengeAttemptRepo.New(txRepo), eventID, teamID, tc.EventChallengeID, tc.ID, in.ReceivedAt); err != nil {
		return SubmitChallengeResult{}, err
	}
	correct := canonicalAnswer == tc.ExpectedFlag
	var before *time.Time
	wasSolved := false
	if correct {
		beforeAt, solved, refreshErr := challengeAttemptRepo.New(txRepo).RefreshSolvedProjection(txCtx, tc.ID, nil)
		if refreshErr != nil {
			err = refreshErr
			return SubmitChallengeResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to refresh challenge result").Err()
		}
		wasSolved = solved
		if solved {
			before = &beforeAt
		}
	}
	a, err := challengeAttempt.New(eventID, teamID, tc.ID, userID, in.Answer, correct, in.ReceivedAt)
	if err != nil {
		return SubmitChallengeResult{}, err
	}
	if _, err = challengeAttemptRepo.New(txRepo).Create(txCtx, a); err != nil {
		return SubmitChallengeResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create challenge attempt").Err()
	}
	first := false
	if correct {
		attempts := challengeAttemptRepo.New(txRepo)
		effective, refreshErr := attempts.EffectiveSolvedAt(txCtx, tc.ID)
		if refreshErr != nil {
			return SubmitChallengeResult{}, model.ErrPlatform.WithError(refreshErr).WithMessage("Failed to calculate challenge result").Err()
		}
		var award *int32
		if effective.Solved && !wasSolved {
			award, refreshErr = fixedAward(txCtx, attempts, tc.ID, effective.SolvedAt)
			if refreshErr != nil {
				return SubmitChallengeResult{}, model.ErrPlatform.WithError(refreshErr).WithMessage("Failed to calculate challenge award").Err()
			}
		}
		afterAt, afterSolved, refreshErr := attempts.RefreshSolvedProjectionValue(txCtx, tc.ID, effective, award)
		if refreshErr != nil {
			err = refreshErr
			return SubmitChallengeResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to refresh challenge result").Err()
		}
		var after *time.Time
		if afterSolved {
			after = &afterAt
		}
		// A hidden team is absent from the public results, so its solve is
		// not announced to the live clients; unhiding it advances the revision.
		visible, visibleErr := eventTeamRepo.New(txRepo).Visible(txCtx, eventID, teamID)
		if visibleErr != nil {
			return SubmitChallengeResult{}, model.ErrPlatform.WithError(visibleErr).WithMessage("Failed to read team visibility").Err()
		}
		switch {
		case !visible:
		case award == nil && afterSolved && !wasSolved:
			err = recordScoreboardRecalculation(txCtx, txRepo, eventID)
		default:
			err = recordResultChange(txCtx, txRepo, eventID, teamID, tc.EventChallengeID, before, after)
		}
		if err != nil {
			return SubmitChallengeResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to record result change").Err()
		}
		first = !wasSolved
	}
	body, marshalErr := json.Marshal(storedChallengeSubmissionResult{Correct: correct, FirstSolve: first})
	if marshalErr != nil {
		return SubmitChallengeResult{}, model.ErrPlatform.WithError(marshalErr).WithMessage("Failed to encode idempotent response").Err()
	}
	completed, completeErr := idempotency.Complete(txCtx, record, 200, body)
	if completeErr != nil {
		return SubmitChallengeResult{}, model.ErrPlatform.WithError(completeErr).WithMessage("Failed to complete idempotency key").Err()
	}
	if !completed {
		return SubmitChallengeResult{}, model.ErrPlatform.WithMessage("Idempotency key was not reserved").Err()
	}
	if err = unit.Save(); err != nil {
		return SubmitChallengeResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to submit challenge").Err()
	}
	return SubmitChallengeResult{Correct: correct, FirstSolve: first}, nil
}

// throttleSubmission enforces the flag rate limits on the attempts already
// stored. Locking the team challenge serializes concurrent submissions of one
// challenge, so a burst cannot pass the count before any of it is recorded;
// the looser team-wide limit may overshoot by the parallel requests at most.
func throttleSubmission(ctx context.Context, attempts *challengeAttemptRepo.Repository, eventID, teamID, challengeID, teamChallengeID uuid.UUID, now time.Time) error {
	if _, err := attempts.LockTeamChallenge(ctx, eventID, teamID, challengeID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to lock team challenge").Err()
	}
	window, err := attempts.ChallengeWindow(ctx, teamChallengeID, challengeAttempt.ChallengeRateLimit, now)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count challenge attempts").Err()
	}
	if err = challengeAttempt.ChallengeRateLimit.Check(window.Attempts, window.Oldest, now); err != nil {
		return err
	}
	window, err = attempts.TeamWindow(ctx, eventID, teamID, challengeAttempt.TeamRateLimit, now)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count team attempts").Err()
	}
	return challengeAttempt.TeamRateLimit.Check(window.Attempts, window.Oldest, now)
}

// requireTeamAdmitted enforces the minimum team size: a team that is not
// admitted keeps its results but gets no challenges, files, labs or VPN.
func requireTeamAdmitted(ctx context.Context, teams *eventTeamRepo.Repository, eventID, teamID uuid.UUID) error {
	admitted, err := teams.Admitted(ctx, eventID, teamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get team admission").Err()
	}
	if !admitted {
		return eventTeamModel.ErrEventTeamNotAdmitted.Err()
	}
	return nil
}

// requireTeamFormed keeps tasks, labs and VPN from a team whose captain has not
// confirmed the roster yet (event_team_formed).
func requireTeamFormed(ctx context.Context, teams *eventTeamRepo.Repository, eventID, teamID uuid.UUID) error {
	formed, err := teams.Formed(ctx, eventID, teamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get team formation").Err()
	}
	if !formed {
		return eventTeamModel.ErrEventTeamNotFormed.Err()
	}
	return nil
}

// The board remains readable after the competition ends so participants can
// review tasks and solutions. Mutations still require an open runtime.
func (u *EventUseCase) requireChallengeBoardVisible(ctx context.Context, eventID uuid.UUID) error {
	event, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event lifecycle").Err()
	}
	status := event.Lifecycle.Status(time.Now())
	if status != eventModel.LifecycleStarted && status != eventModel.LifecycleFinished {
		return eventModel.ErrEventRuntimeNotOpen.Err()
	}
	return nil
}

func selectTeamVariant(teamID uuid.UUID, attachment eventExerciseModel.EventExercise, version exerciseModel.ExerciseVersion) (int32, error) {
	if len(version.Variants) == 0 {
		return 0, model.ErrPlatform.WithMessage("Pinned exercise version has no variants").Err()
	}
	if attachment.VariantMode == eventExerciseModel.VariantModeFixed {
		if attachment.FixedVariantIndex == nil || *attachment.FixedVariantIndex >= int32(len(version.Variants)) {
			return 0, model.ErrPlatform.WithMessage("Pinned fixed variant is unavailable").Err()
		}
		return *attachment.FixedVariantIndex, nil
	}
	return teamChallengeModel.SelectVariant(teamID, attachment.ID, len(version.Variants))
}
