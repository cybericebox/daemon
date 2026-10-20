package event

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"

	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
)

func TestBulkExerciseIDsDedupesAndBoundsTheSelection(t *testing.T) {
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	got, err := bulkExerciseIDs([]uuid.UUID{a, b, a, uuid.Nil})
	if err != nil || len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("bulkExerciseIDs = %v, %v", got, err)
	}
	if _, err = bulkExerciseIDs(nil); !errors.Is(err, eventExerciseModel.ErrEventExerciseBulkSize.Err()) {
		t.Fatalf("empty selection: %v", err)
	}
	many := make([]uuid.UUID, MaxBulkExercises+1)
	for i := range many {
		many[i] = uuid.Must(uuid.NewV7())
	}
	if _, err = bulkExerciseIDs(many); !errors.Is(err, eventExerciseModel.ErrEventExerciseBulkSize.Err()) {
		t.Fatalf("201 items: %v", err)
	}
	if _, err = bulkExerciseIDs(many[:MaxBulkExercises]); err != nil {
		t.Fatalf("200 items: %v", err)
	}
}

// One failing item never stops the others: every item gets its own result, in order.
func TestEachExerciseKeepsGoingAfterAFailure(t *testing.T) {
	a, b, c := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	refused := eventExerciseModel.ErrEventExerciseDetachNeedsConfirm.Err()
	var called []uuid.UUID
	results, err := eachExercise(context.Background(), []uuid.UUID{a, b, c}, func(id uuid.UUID) error {
		called = append(called, id)
		if id == b {
			return refused
		}
		return nil
	})
	if err != nil || len(results) != 3 || len(called) != 3 {
		t.Fatalf("results = %+v, err %v, called %v", results, err, called)
	}
	if results[0].Err != nil || !errors.Is(results[1].Err, refused) || results[2].Err != nil {
		t.Fatalf("results = %+v", results)
	}
}
