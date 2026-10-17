package exercise

import (
	"context"
	"slices"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
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
	counts, err := u.exercises.VariantStats(ctx, exerciseID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list variant statistics").Err()
	}

	type variantKey struct{ version, variant uuid.UUID }
	type taskKey struct{ version, variant, task uuid.UUID }
	issuedBy := make(map[variantKey]int64, len(counts.Issued))
	for _, c := range counts.Issued {
		issuedBy[variantKey{c.VersionID, c.VariantID}] = c.Teams
	}
	taskBy := make(map[taskKey]int64, len(counts.TaskSolves))
	for _, c := range counts.TaskSolves {
		taskBy[taskKey{c.VersionID, c.VariantID, c.TaskID}] = c.Teams
	}
	depthsBy := make(map[variantKey][]exerciseRepo.VariantSolveDepth)
	for _, d := range counts.Depths {
		key := variantKey{d.VersionID, d.VariantID}
		depthsBy[key] = append(depthsBy[key], d)
	}

	out := make([]VersionVariantStats, 0)
	for _, version := range versions {
		stats := VersionVariantStats{VersionID: version.ID, Status: string(version.Status), CreatedAt: version.CreatedAt,
			PublishedAt: version.PublishedAt, Variants: make([]VariantStats, 0, len(version.Variants))}
		var issued int64
		for i, variant := range version.Variants {
			key := variantKey{version.ID, variant.ID}
			issued += issuedBy[key]
			tasks := make([]TaskSolves, 0, len(variant.Tasks))
			for _, task := range variant.Tasks {
				tasks = append(tasks, TaskSolves{TaskID: task.ID, Name: task.Name, Solved: taskBy[taskKey{version.ID, variant.ID, task.ID}]})
			}
			stats.Variants = append(stats.Variants, VariantStats{VariantID: variant.ID, Number: i + 1, Issued: issuedBy[key],
				Solved: fullySolved(depthsBy[key], len(variant.Tasks)), Tasks: tasks})
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

// fullySolved counts the teams that solved at least as many distinct subtasks as the variant has: «solved» = the
// team solved every subtask of the variant at that event. A variant without subtasks is never solved.
func fullySolved(depths []exerciseRepo.VariantSolveDepth, taskCount int) int64 {
	if taskCount == 0 {
		return 0
	}
	var teams int64
	for _, d := range depths {
		if d.SolvedTasks >= int64(taskCount) {
			teams += d.Teams
		}
	}
	return teams
}

// statsTime orders versions: published ones by their publication, the others by creation.
func statsTime(v VersionVariantStats) time.Time {
	if v.PublishedAt != nil {
		return *v.PublishedAt
	}
	return v.CreatedAt
}
