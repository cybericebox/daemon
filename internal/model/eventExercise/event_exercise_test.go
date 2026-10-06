package eventExerciseModel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
)

func TestNew_RequiresConsistentVariantMode(t *testing.T) {
	now := time.Now()
	eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	index := int32(0)
	if _, err := eventExerciseModel.New(eventID, exerciseID, versionID, eventExerciseModel.VariantModeFixed, &index, now, uuid.Nil); err != nil {
		t.Fatalf("fixed variant: %v", err)
	}
	if _, err := eventExerciseModel.New(eventID, exerciseID, versionID, eventExerciseModel.VariantModePerTeam, &index, now, uuid.Nil); !errors.Is(err, eventExerciseModel.ErrEventExerciseVariantModeInvalid.Err()) {
		t.Fatalf("want inconsistent per-team mode error, got %v", err)
	}
}
