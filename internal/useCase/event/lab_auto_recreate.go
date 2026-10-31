package event

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/autoRecreateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// autoRecreateFailedLabs recreates the Labs the agent accepted and that then failed (a crash loop, a failed phase, not
// ready in time) with the next generation, up to autoRecreateRepo.MaxAttempts times, after 1, 5 and 15 minutes. The
// new generation starts from the image: the state of the failed one stays in its archived generation and is not
// restored. A Lab that is stopping or closed, one that is solved and an event that has finished are left alone. The
// count is per team and exercise, is reset when the Lab becomes ready or the organizer recreates the stand, and a
// failed Lab that used its attempts stays failed: the stand fails with the last reason and the organizers hear it
// once (the failed counters of the stand skip a Lab that can still be recreated).
func (u *EventUseCase) autoRecreateFailedLabs(ctx context.Context, e eventModel.Event, now time.Time) error {
	if u.autoRecreate == nil || !u.lifecycleControls || u.uow == nil {
		return nil
	}
	candidates, err := u.autoRecreate.Candidates(ctx, e.ID, now)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list failed stand labs for recreation").Err()
	}
	var errs []error
	for _, c := range candidates {
		// Tracked from the first pass that sees the failure: the stand counts the Lab as being recreated.
		if trackErr := u.autoRecreate.Track(ctx, c.TeamID, c.ExerciseID, c.Reason, now); trackErr != nil {
			errs = append(errs, model.ErrPlatform.WithError(trackErr).WithMessage("Failed to track a failed stand lab").Err())
			continue
		}
		if now.Sub(c.FailedAt) < eventStandModel.AutoRecreateBackoff(c.Attempts) {
			continue
		}
		recreated, recreateErr := u.recreateFailedLab(ctx, e.ID, c, now)
		if recreateErr != nil {
			errs = append(errs, recreateErr)
			continue
		}
		if !recreated {
			continue
		}
		if bumpErr := u.autoRecreate.Bump(ctx, c.TeamID, c.ExerciseID, c.Reason, now); bumpErr != nil {
			errs = append(errs, model.ErrPlatform.WithError(bumpErr).WithMessage("Failed to count a stand lab recreation").Err())
			continue
		}
		log.Info().Str("event_id", e.ID.String()).Str("team_id", c.TeamID.String()).Int("attempt", c.Attempts+1).
			Str("last_failure", c.Reason).Msg("Recreating a failed stand lab with the next generation")
	}
	return errors.Join(errs...)
}

// recreateFailedLab closes the failed Lab of the candidate and gives its bindings the next generation, the way the
// organizer's recreate does for one Lab. It reports whether it did (false: the Lab changed meanwhile).
func (u *EventUseCase) recreateFailedLab(ctx context.Context, eventID uuid.UUID, c autoRecreateRepo.Candidate, now time.Time) (bool, error) {
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return false, err
	}
	defer unit.Restore()
	if _, err = q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
		return false, err
	}
	if err = eventLabRepo.New(q).LockAdmission(txCtx, c.TeamID); err != nil {
		return false, err
	}
	bindings, err := labBindingRepo.New(q).ListTeamLive(txCtx, c.TeamID)
	if err != nil {
		return false, err
	}
	var leader *labBindingModel.Binding
	for i := range bindings {
		if bindings[i].ID == c.BindingID {
			leader = &bindings[i]
		}
	}
	if leader == nil || !leader.LabID.Valid || leader.Readiness != labBindingModel.ReadinessFailed {
		return false, nil
	}
	l, err := eventLabRepo.New(q).Lock(txCtx, leader.LabID.UUID)
	if err != nil {
		return false, err
	}
	if l.CloseReason == "solved" || l.ClosedAt != nil || l.DesiredState != "Running" {
		return false, nil // stopping, closed or solved: not recreated
	}
	expected := l.Revision
	if err = l.Close("manual", uuid.Must(uuid.NewV7()), now); err != nil {
		return false, err
	}
	l.SetRetentionDeadline(now.Add(time.Duration(l.RetentionMinutes)*time.Minute), now)
	if _, err = eventLabRepo.New(q).Update(txCtx, l, expected); err != nil {
		return false, err
	}
	if err = eventLabRetentionRepo.New(q).Archive(txCtx, l.ID); err != nil {
		return false, err
	}
	// Every binding of the Lab (the tasks of the exercise share it) moves to the next generation together.
	for _, b := range bindings {
		if !b.LabID.Valid || b.LabID.UUID != l.ID {
			continue
		}
		next, genErr := eventLabRepo.New(q).NextGeneration(txCtx, b.EventTeamID, b.EventChallengeID, b.Generation)
		if genErr != nil {
			return false, genErr
		}
		if _, err = labBindingRepo.New(q).Recreate(txCtx, b, labBindingModel.NextLabName(b.LabName, next), next); err != nil {
			return false, err
		}
	}
	if err = unit.Save(); err != nil {
		return false, err
	}
	u.wakeLabLifecycle(ctx)
	return true, nil
}

// forgetAutoRecreate resets the count of a Lab that became ready.
func (u *EventUseCase) forgetAutoRecreate(ctx context.Context, teamID, exerciseID uuid.UUID) {
	if u.autoRecreate == nil {
		return
	}
	if err := u.autoRecreate.Reset(ctx, teamID, exerciseID); err != nil {
		log.Warn().Err(err).Str("team_id", teamID.String()).Msg("Failed to reset the recreation count of a ready stand lab")
	}
}
