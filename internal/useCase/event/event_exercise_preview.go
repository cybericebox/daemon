package event

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// EventExerciseUpdateTeam is a team whose stand the update would recreate.
type EventExerciseUpdateTeam struct {
	ID   uuid.UUID
	Name string
}

// EventExerciseUpdatePreview is the dry run of «Оновити до нової версії»: what the update would do, with nothing
// changed. The organizer confirms (RecreateStands) only when RequiresConfirmation is set.
type EventExerciseUpdatePreview struct {
	TargetVersionID uuid.UUID
	// StandsRecreated labs of TeamsAffected teams run something the new version changes: they are recreated and the
	// teams lose lab access until the new labs are ready. StandsReset labs have not started yet and are re-prepared
	// in place; StandsKept labs run what the new version still says and stay as they are.
	StandsRecreated int
	TeamsAffected   int
	StandsReset     int
	StandsKept      int
	Teams           []EventExerciseUpdateTeam
	// Tasks by name: added to the set, removed from it, kept under a new task id (a re-import).
	TasksAdded     []string
	TasksRemoved   []string
	TasksRepointed []string
	// TasksBlocking are removed tasks that already have attempts: the update is refused until they are handled.
	TasksBlocking        []string
	RequiresConfirmation bool
	// ReservationShort: the reservation does not hold the new size for all teams (the update would be refused).
	ReservationShort bool
}

// PreviewEventExerciseUpdate says what UpdateEventExercise would do for the same arguments, and changes nothing.
func (u *EventUseCase) PreviewEventExerciseUpdate(ctx context.Context, eventID, eventExerciseID uuid.UUID, versionID *uuid.UUID) (EventExerciseUpdatePreview, error) {
	link, err := u.activeAttachment(ctx, eventID, eventExerciseID)
	if err != nil {
		return EventExerciseUpdatePreview{}, err
	}
	target := uuid.Nil
	if versionID != nil {
		target = *versionID
	} else {
		entry, getErr := u.exercises.GetByID(ctx, link.ExerciseID)
		if getErr != nil {
			return EventExerciseUpdatePreview{}, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get exercise").Err()
		}
		if !entry.PublishedVersionID.Valid {
			return EventExerciseUpdatePreview{}, eventExerciseModel.ErrEventExerciseVersionNotPublished.Err()
		}
		target = entry.PublishedVersionID.UUID
	}
	version, _, err := u.attachableVersion(ctx, eventID, target, true)
	if err != nil {
		return EventExerciseUpdatePreview{}, err
	}
	if version.ExerciseID != link.ExerciseID {
		return EventExerciseUpdatePreview{}, eventExerciseModel.ErrEventExerciseVersionMismatch.Err()
	}
	now := time.Now()
	preview := EventExerciseUpdatePreview{TargetVersionID: version.ID, Teams: []EventExerciseUpdateTeam{}, TasksAdded: []string{}, TasksRemoved: []string{}, TasksRepointed: []string{}, TasksBlocking: []string{}}
	if reserveErr := u.requireReservedForChange(ctx, eventID, link, version, now); reserveErr != nil {
		if !errors.Is(reserveErr, calModel.ErrNotEnoughReserved.Err()) {
			return EventExerciseUpdatePreview{}, reserveErr
		}
		preview.ReservationShort = true
	}

	board, err := eventChallengeRepo.New(u.repo).List(ctx, link.ID)
	if err != nil {
		return EventExerciseUpdatePreview{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	match := matchBoardTasks(board, version.Variants[link.CanonicalVariant(len(version.Variants))].Tasks)
	nameOf := map[uuid.UUID]string{}
	for _, challenge := range board {
		nameOf[challenge.ID] = snapshotTaskName(challenge.Snapshot)
	}
	for _, id := range match.Removed {
		preview.TasksRemoved = append(preview.TasksRemoved, nameOf[id])
	}
	for _, task := range match.Added {
		preview.TasksAdded = append(preview.TasksAdded, task.Name)
	}
	for id := range match.Repointed {
		preview.TasksRepointed = append(preview.TasksRepointed, nameOf[id])
	}
	attempted, err := eventChallengeRepo.New(u.repo).WithAttempts(ctx, match.Removed)
	if err != nil {
		return EventExerciseUpdatePreview{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check challenge attempts").Err()
	}
	for id := range attempted {
		preview.TasksBlocking = append(preview.TasksBlocking, nameOf[id])
	}

	plan, err := u.standRecreationPlan(ctx, u.repo, nil, link, version, now)
	if err != nil {
		return EventExerciseUpdatePreview{}, err
	}
	if plan != nil {
		preview.StandsRecreated, preview.TeamsAffected = plan.standsToRecreate()
		for _, move := range plan.moves {
			preview.StandsReset += len(move.unborn)
			preview.StandsKept += move.kept
			if len(move.labs) > 0 {
				preview.Teams = append(preview.Teams, EventExerciseUpdateTeam{ID: move.team.TeamID, Name: move.team.PublicName})
			}
		}
	}
	preview.RequiresConfirmation = preview.TeamsAffected > 0
	return preview, nil
}
