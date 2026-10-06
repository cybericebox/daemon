package seed

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/gofrs/uuid"

	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// publishedExercise is a catalog exercise the seeder keeps published.
type publishedExercise struct {
	Spec       exerciseSpec
	ExerciseID uuid.UUID
	VersionID  uuid.UUID
}

// fingerprint marks the content of a published version, so an unchanged definition is skipped
// on a re-run and a changed one is published as a new version.
func (e exerciseSpec) fingerprint() string {
	sum := sha256.Sum256(mustJSON(struct {
		Spec     exerciseSpec
		Variants any
	}{e, e.variants()}))
	return "seed:" + hex.EncodeToString(sum[:])[:16]
}

// ensureExercise finds the catalog exercise by its name, creates it when missing, and publishes
// its definition unless the published version already carries this definition.
func (s *Seeder) ensureExercise(ctx context.Context, spec exerciseSpec, by uuid.UUID) (publishedExercise, string, error) {
	result := publishedExercise{Spec: spec}
	action := "kept"
	found, err := s.exercises.ListExercises(ctx, exerciseUseCase.ExercisesFilter{Search: spec.Name, Tags: []string{MarkerTag}, Archived: "exclude", PageSize: 50})
	if err != nil {
		return result, "", fmt.Errorf("find exercise %q: %w", spec.Name, err)
	}
	var view exerciseUseCase.ExerciseView
	for _, item := range found.Exercises {
		if item.Name == spec.Name {
			if view, err = s.exercises.GetExercise(ctx, item.ID); err != nil {
				return result, "", fmt.Errorf("get exercise %q: %w", spec.Name, err)
			}
			break
		}
	}
	if view.ID == uuid.Nil {
		if view, err = s.exercises.CreateExercise(ctx, exerciseUseCase.CreateExerciseInput{Name: spec.Name, Description: spec.Description, Tags: spec.Tags, CreatedBy: by}); err != nil {
			return result, "", fmt.Errorf("create exercise %q: %w", spec.Name, err)
		}
		action = "created"
	} else if view.Description != spec.Description || !slices.Equal(view.Tags, spec.Tags) {
		if view, err = s.exercises.UpdateExerciseIdentity(ctx, exerciseUseCase.UpdateExerciseInput{ID: view.ID, Name: spec.Name, Description: spec.Description, Tags: spec.Tags, UpdatedBy: by}); err != nil {
			return result, "", fmt.Errorf("update exercise %q: %w", spec.Name, err)
		}
		action = "updated"
	}
	result.ExerciseID = view.ID

	marker := spec.fingerprint()
	if view.PublishedVersionID != nil {
		current, getErr := s.exercises.GetVersion(ctx, view.ID, *view.PublishedVersionID)
		if getErr != nil {
			return result, "", fmt.Errorf("get published version of %q: %w", spec.Name, getErr)
		}
		if current.AdminNote == marker {
			result.VersionID = current.ID
			return result, action, nil
		}
	}
	if _, err = s.exercises.SaveDraft(ctx, view.ID, exerciseUseCase.SaveDraftInput{AdminNote: marker, Variants: spec.variants(), SavedBy: by}); err != nil {
		return result, "", fmt.Errorf("save draft of %q: %w", spec.Name, err)
	}
	version, err := s.exercises.PublishDraft(ctx, view.ID)
	if err != nil {
		return result, "", fmt.Errorf("publish %q: %w", spec.Name, err)
	}
	result.VersionID = version.ID
	if action == "kept" {
		action = "republished"
	}
	return result, action, nil
}
