package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
)

// ReadinessView is the organizers' one look before the start: which tasks participants still do not see and which
// stands are not ready, with what each waits for.
type ReadinessView struct {
	// Ready: nothing to warn about.
	Ready bool
	// StartAt is the event start (nil: no schedule yet); Started: it has begun.
	StartAt *time.Time
	Started bool
	// UnpublishedTasks are the tasks of the active sets that are not published to participants.
	UnpublishedTasks []ReadinessTaskView
	// StandsApplicable: the event uses stands. Stands lists the teams whose stand is not ready (the moderators
	// team included) with the labs that are not ready; StandsTotal / StandsReady count every team stand.
	StandsApplicable bool
	StandsTotal      int
	StandsReady      int
	Stands           []StandTeamView
}

type ReadinessTaskView struct {
	ChallengeID uuid.UUID
	Name        string
	// ExerciseID is the set the task belongs to; StageID its stage, if any.
	ExerciseID uuid.UUID
	StageID    *uuid.UUID
}

// GetEventReadiness collects the unpublished tasks and the stands that are not ready for the event.
func (u *EventUseCase) GetEventReadiness(ctx context.Context, eventID uuid.UUID) (ReadinessView, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ReadinessView{}, eventModel.ErrEventNotFound.Err()
		}
		return ReadinessView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	now := time.Now()
	view := ReadinessView{UnpublishedTasks: []ReadinessTaskView{}, Stands: []StandTeamView{}}
	if e.Lifecycle.Configured {
		start := e.Lifecycle.StartAt
		view.StartAt, view.Started = &start, !now.Before(start)
	}
	tasks, err := u.eventChallenges.UnpublishedForEvent(ctx, eventID)
	if err != nil {
		return ReadinessView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list unpublished tasks").Err()
	}
	for _, task := range tasks {
		view.UnpublishedTasks = append(view.UnpublishedTasks, ReadinessTaskView{ChallengeID: task.ID, Name: task.Name, ExerciseID: task.EventExerciseID, StageID: task.StageID})
	}
	if e.InfrastructureAllowed {
		stands, standsErr := u.GetEventStands(ctx, eventID)
		if standsErr != nil {
			return ReadinessView{}, standsErr
		}
		view.StandsApplicable, view.StandsTotal, view.StandsReady = true, stands.Summary.Total, stands.Summary.Ready
		for _, item := range stands.Items {
			if item.Status == eventStandModel.StatusReady {
				continue
			}
			view.Stands = append(view.Stands, item)
		}
	}
	view.Ready = len(view.UnpublishedTasks) == 0 && len(view.Stands) == 0
	return view, nil
}
