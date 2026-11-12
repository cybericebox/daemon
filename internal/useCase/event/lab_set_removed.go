package event

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
)

// retireRemovedSetLab ends a canonical Lab whose event exercise is gone (the set
// was deleted) or detached: the Lab is closed as a manual stop with its retention
// due now, so the lifecycle stops it, the retention sweep deletes it, and the
// lifecycle passes of the event stop looking for the missing set. The Lab row is
// the history of a stand; it is not deleted. A solved or already deleted Lab keeps
// its state. The Lab must be locked by the caller. It reports whether the row changed.
func retireRemovedSetLab(ctx context.Context, repo IRepository, l eventLabModel.Lab, now time.Time) (bool, error) {
	if l.CloseReason == "solved" || l.DesiredState == "Deleted" {
		return false, nil
	}
	expected := l.Revision
	changed := false
	if l.DesiredState == "Running" {
		if err := l.Close("manual", uuid.Must(uuid.NewV7()), now); err != nil {
			return false, fmt.Errorf("close lab %s of a removed set: %w", l.ID, err)
		}
		changed = true
	}
	if l.RetentionUntil == nil || l.RetentionUntil.After(now) {
		l.SetRetentionDeadline(now, now)
		changed = true
	}
	if !changed {
		return false, nil
	}
	ok, err := eventLabRepo.New(repo).Update(ctx, l, expected)
	if err != nil {
		return false, fmt.Errorf("update lab %s of a removed set: %w", l.ID, err)
	}
	if !ok {
		return false, fmt.Errorf("lab %s of a removed set: revision changed", l.ID)
	}
	log.Info().Str("lab_id", l.ID.String()).Str("event_id", l.EventID.String()).Str("event_exercise_id", l.EventExerciseID.String()).
		Msg("Closed a lab whose set was removed from the event")
	return true, nil
}

// closeSetLabsInTransaction retires every canonical Lab of an event exercise that
// leaves the event. It takes the source-change locks first (event, teams, Labs),
// the order every other source change uses, and returns the Labs it changed.
func (u *EventUseCase) closeSetLabsInTransaction(ctx context.Context, repo IRepository, eventID, eventExerciseID uuid.UUID, now time.Time) ([]eventLabModel.Lab, error) {
	labs, err := eventLabRepo.New(repo).LockSetLabs(ctx, eventID, eventExerciseID)
	if err != nil {
		return nil, fmt.Errorf("lock labs of event exercise %s: %w", eventExerciseID, err)
	}
	var closed []eventLabModel.Lab
	for _, l := range labs {
		changed, err := retireRemovedSetLab(ctx, repo, l, now)
		if err != nil {
			return nil, err
		}
		if changed {
			closed = append(closed, l)
		}
	}
	return closed, nil
}

// deleteClosedSetLabs removes the closed Labs from the agent when the lifecycle
// controls are off (with them on, the lifecycle stops and the retention sweep
// deletes). A failure only leaves the Lab to the cleanup sweeps.
func (u *EventUseCase) deleteClosedSetLabs(ctx context.Context, labs []eventLabModel.Lab) {
	if u.lifecycleControls || u.infra == nil {
		return
	}
	deleter, ok := u.infra.(standLabDeleter)
	if !ok {
		return
	}
	for _, l := range labs {
		if err := deleter.DeleteLab(ctx, l.Ref.Group, l.Ref.Lab); err != nil {
			log.Error().Err(err).Str("lab_group", l.Ref.Group).Str("lab", l.Ref.Lab).Msg("Failed to delete a lab of a detached set")
		}
	}
}
