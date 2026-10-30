package exercise

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func importedLinkedTask(flag string, files ...exerciseModel.AttachmentRef) exerciseModel.Task {
	return exerciseModel.Task{
		ID: uuid.Must(uuid.NewV7()), Name: "Pwn", Difficulty: exerciseModel.DifficultyEasy, Flag: []string{flag}, Attachments: files,
		Description: json.RawMessage(`{"root":{"children":[{"type":"paragraph","children":[{"type":"text","text":"Pwn it"}]}]}}`),
	}
}

// An archive version marked published meets the linked-variant gates: files on the base only block the import and
// the error names the variant, the version is the error's field.
func TestRequireImportedPublishable_LinkedVariantWithoutFiles(t *testing.T) {
	baseID, linkedID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	file := exerciseModel.AttachmentRef{FileID: uuid.Must(uuid.NewV7()), Name: "pwn"}
	linkedTask := importedLinkedTask("ICE{b}")
	baseTask := importedLinkedTask("ICE{a}", file)
	linkedTask.ID = baseTask.ID
	versions := []exerciseModel.ExerciseVersion{{
		Status: exerciseModel.VersionStatusPublished,
		Variants: []exerciseModel.Variant{
			{ID: baseID, Tasks: []exerciseModel.Task{baseTask}},
			{ID: linkedID, LinkedTo: uuid.NullUUID{UUID: baseID, Valid: true}, Tasks: []exerciseModel.Task{linkedTask}},
		},
	}}

	err := (&ExerciseUseCase{}).requireImportedPublishable(context.Background(), versions, nil)
	require.True(t, errors.Is(err, exerciseModel.ErrLinkedAttachmentMissing.Err()), "got %v", err)
	var carrier interface{ PublicContext() map[string]any }
	require.True(t, errors.As(err, &carrier))
	assert.Equal(t, map[string]any{"variant": 2, "task": "Pwn", "field": "versions[1]"}, carrier.PublicContext())

	// A draft in the same archive may stay incomplete.
	versions[0].Status = exerciseModel.VersionStatusDraft
	require.NoError(t, (&ExerciseUseCase{}).requireImportedPublishable(context.Background(), versions, nil))
}
