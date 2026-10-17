package imageRegistryRepo

import (
	"context"

	"github.com/gofrs/uuid"
)

// EventExerciseImages is a read-side shape: one active attachment of an event
// and the distinct device images of its pinned version (all variants).
type EventExerciseImages struct {
	EventExerciseID uuid.UUID
	ExerciseID      uuid.UUID
	ExerciseName    string
	Images          []string
}

// EventExerciseImages lists the event's active attachments with their images
// (a deliberate narrow read for the availability check).
func (r *Repository) EventExerciseImages(ctx context.Context, eventID uuid.UUID) ([]EventExerciseImages, error) {
	rows, err := r.q.ListEventExerciseImages(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]EventExerciseImages, 0, len(rows))
	for _, row := range rows {
		out = append(out, EventExerciseImages{
			EventExerciseID: row.EventExerciseID, ExerciseID: row.ExerciseID,
			ExerciseName: row.ExerciseName, Images: row.Images,
		})
	}
	return out, nil
}
