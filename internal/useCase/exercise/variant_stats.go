package exercise

import (
	"context"
	"slices"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
)

// GetVariantStats returns the grow-only issue and solve counters of the exercise's variants, per version: only
// versions issued at least once, newest first; every variant of such a version in order and every subtask of it,
// zeros where nothing was counted. Counters of a variant id the version does not have are dropped. Route gate:
// exercises.read.
func (u *ExerciseUseCase) GetVariantStats(ctx context.Context, exerciseID uuid.UUID) ([]VersionVariantStats, error) {
	versions, err := u.exercises.ListVersions(ctx, exerciseID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list exercise versions").Err()
	}
	counters, taskCounters, err := u.exercises.VariantStats(ctx, exerciseID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list variant statistics").Err()
	}

	type variantKey struct{ version, variant uuid.UUID }
	type taskKey struct{ version, variant, task uuid.UUID }
	byVariant := make(map[variantKey][2]int64, len(counters))
	for _, c := range counters {
		byVariant[variantKey{c.VersionID, c.VariantID}] = [2]int64{c.Issued, c.Solved}
	}
	byTask := make(map[taskKey]int64, len(taskCounters))
	for _, c := range taskCounters {
		byTask[taskKey{c.VersionID, c.VariantID, c.TaskID}] = c.Solved
	}

	out := make([]VersionVariantStats, 0)
	for _, version := range versions {
		stats := VersionVariantStats{VersionID: version.ID, Status: string(version.Status), CreatedAt: version.CreatedAt,
			PublishedAt: version.PublishedAt, Variants: make([]VariantStats, 0, len(version.Variants))}
		var issued int64
		for i, variant := range version.Variants {
			counts := byVariant[variantKey{version.ID, variant.ID}]
			issued += counts[0]
			tasks := make([]TaskSolves, 0, len(variant.Tasks))
			for _, task := range variant.Tasks {
				tasks = append(tasks, TaskSolves{TaskID: task.ID, Name: task.Name, Solved: byTask[taskKey{version.ID, variant.ID, task.ID}]})
			}
			stats.Variants = append(stats.Variants, VariantStats{VariantID: variant.ID, Number: i + 1, Issued: counts[0], Solved: counts[1], Tasks: tasks})
		}
		if issued > 0 {
			out = append(out, stats)
		}
	}
	slices.SortStableFunc(out, func(a, b VersionVariantStats) int {
		return statsTime(b).Compare(statsTime(a))
	})
	return out, nil
}

// statsTime orders versions: published ones by their publication, the others by creation.
func statsTime(v VersionVariantStats) time.Time {
	if v.PublishedAt != nil {
		return *v.PublishedAt
	}
	return v.CreatedAt
}
