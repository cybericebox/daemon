package exercise

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// importIdentity pairs the imported version with the stored working copy of the target exercise (see
// matchStoredIdentity). The stored copy is read through a synced copy: a linked variant of it holds only its own
// parts, and its devices are the base's.
func importIdentity(
	in *exerciseModel.ExerciseVersion,
	stored exerciseModel.ExerciseVersion,
) (exerciseModel.ImportDiff, []exerciseModel.ImportWarning, error) {
	copied, err := copyImportVersion(stored)
	if err != nil {
		return exerciseModel.ImportDiff{}, nil, err
	}
	if err = copied.SyncLinkedVariants(); err != nil {
		return exerciseModel.ImportDiff{}, nil, err
	}
	return matchStoredIdentity(in, copied)
}

// newImportDiff is the report of an import that creates an exercise: everything is added.
func newImportDiff(version exerciseModel.ExerciseVersion) exerciseModel.ImportDiff {
	diff := exerciseModel.ImportDiff{
		Variants: make([]exerciseModel.ImportVariantDiff, 0, len(version.Variants)),
	}
	for i, variant := range version.Variants {
		entry := exerciseModel.ImportVariantDiff{
			Status:   exerciseModel.ImportVariantAdded,
			Position: i + 1,
			Tasks:    emptyChanges(),
			Devices:  emptyChanges(),
		}
		for _, task := range variant.Tasks {
			entry.Tasks.Added = append(
				entry.Tasks.Added,
				exerciseModel.ImportItem{Name: task.Name, Key: task.Key},
			)
		}
		for _, device := range variant.Topology.Devices {
			entry.Devices.Added = append(
				entry.Devices.Added,
				exerciseModel.ImportItem{Name: device.Name},
			)
		}
		diff.Variants = append(diff.Variants, entry)
	}
	return diff
}

// importReport assembles the report of one item: the identity pairing (diff, warnings so far), the errors that
// would stop the result being published, and, for an existing exercise, who uses it. It writes nothing.
func (u *ExerciseUseCase) importReport(
	ctx context.Context,
	target *exerciseModel.Exercise,
	version exerciseModel.ExerciseVersion,
	diff exerciseModel.ImportDiff,
	warnings []exerciseModel.ImportWarning,
	noExerciseID bool,
) (exerciseModel.ImportReport, error) {
	report := exerciseModel.ImportReport{
		Diff:     diff,
		Warnings: append([]exerciseModel.ImportWarning{}, warnings...),
		Usage:    exerciseModel.ImportUsage{Events: []exerciseModel.ImportUsageEvent{}},
	}
	scope := exerciseModel.Exercise{Scope: exerciseModel.ScopeCatalog}
	if target != nil {
		scope = *target
	}
	report.Issues = u.publishIssues(ctx, scope, version)
	report.Publishable = len(report.Issues) == 0
	if noExerciseID {
		report.Warnings = append(
			report.Warnings,
			exerciseModel.ImportWarning{Code: exerciseModel.ImportWarnNoExerciseID},
		)
	}
	if target == nil {
		return report, nil
	}
	usage, err := u.importUsage(ctx, *target, version)
	if err != nil {
		return report, err
	}
	report.Usage = usage
	if len(usage.Events) > 0 {
		report.Warnings = append(
			report.Warnings,
			exerciseModel.ImportWarning{
				Code:    exerciseModel.ImportWarnUsedByEvents,
				Context: map[string]any{"events": len(usage.Events)},
			},
		)
	}
	running := usage.TestDeploys
	for _, event := range usage.Events {
		running += event.RunningLabs
		for _, task := range event.Removed {
			if task.Attempts > 0 {
				report.Warnings = append(
					report.Warnings,
					exerciseModel.ImportWarning{
						Code: exerciseModel.ImportWarnRemovedHasAttempts,
						Context: map[string]any{
							"event":    event.Name,
							"task":     task.Name,
							"attempts": task.Attempts,
							"solves":   task.Solves,
						},
					},
				)
			}
		}
	}
	if running > 0 {
		report.Warnings = append(
			report.Warnings,
			exerciseModel.ImportWarning{
				Code:    exerciseModel.ImportWarnRunningLabs,
				Context: map[string]any{"labs": running},
			},
		)
	}
	return report, nil
}

// publishIssues lists what keeps the version from being published: the errors of the whole version, then those of
// each variant on its own (so a second broken variant is not hidden behind the first), then the platform gates
// (profiles, infrastructure, resources). Each carries the path of what it names. Two issues that say the same about
// the same place are one.
func (u *ExerciseUseCase) publishIssues(
	ctx context.Context,
	owner exerciseModel.Exercise,
	version exerciseModel.ExerciseVersion,
) []exerciseModel.ImportIssue {
	issues := []exerciseModel.ImportIssue{}
	seen := map[string]bool{}
	add := func(failure error, variantPosition int) {
		issue, ok := issueFrom(failure, version.Variants, variantPosition)
		if !ok {
			return
		}
		key := fmt.Sprintf("%d|%s|%s", issue.Code, issue.Path, issue.Message)
		if !seen[key] {
			seen[key] = true
			issues = append(issues, issue)
		}
	}
	whole := version
	if err := whole.ValidateForPublish(); err != nil {
		add(err, 0)
	}
	for i, variant := range version.Variants {
		single := exerciseModel.ExerciseVersion{
			Name:        version.Name,
			Description: version.Description,
			Tags:        version.Tags,
			Variants:    []exerciseModel.Variant{variant},
		}
		single.Variants[0].LinkedTo = uuid.NullUUID{}
		if err := single.ValidateForPublish(); err != nil {
			add(err, i+1)
		}
	}
	if err := u.requireProfilesSupported(ctx, version.Variants); err != nil {
		add(err, 0)
	}
	if err := u.requireInfrastructureAllowed(ctx, owner, version.Variants); err != nil {
		add(err, 0)
	}
	if err := u.requireResourcesAllowed(ctx, owner.ID, version.Variants); err != nil {
		add(err, 0)
	}
	return issues
}

// issueFrom turns an application error into an issue. The path is where the error says it is: the variant (by
// the id in its public context, else the position of the variant that was being checked alone), and the device.
func issueFrom(
	failure error,
	variants []exerciseModel.Variant,
	variantPosition int,
) (exerciseModel.ImportIssue, bool) {
	var coded appErr.Error
	if !errors.As(failure, &coded) {
		return exerciseModel.ImportIssue{}, false
	}
	status := coded.StatusCode()
	issue := exerciseModel.ImportIssue{Code: status.FullCode(), Message: status.Message()}
	public := coded.PublicContext()
	if len(public) > 0 {
		issue.Context = map[string]any{}
		for key, value := range public {
			if key != "variant" {
				issue.Context[key] = value
			}
		}
	}
	path := ""
	if id, ok := public["variant"].(string); ok && id != "" {
		path = variantPath(variants, id, public["device"])
	} else if variantPosition > 0 {
		path = fmt.Sprintf(".variants[%d]", variantPosition)
		if name, named := public["device"].(string); named {
			for di, device := range variants[variantPosition-1].Topology.Devices {
				if device.Name == name {
					path = fmt.Sprintf("%s.topology.devices[%d]", path, di+1)
					break
				}
			}
		}
	}
	issue.Path = strings.TrimPrefix(path, ".")
	return issue, true
}

// importUsage reads who uses the exercise and what the result would change for each attachment, by the rule an
// event applies when its organizer moves it to a new version: board challenges follow the task id, and a task whose
// id is gone follows its name. Nothing here happens before the draft is published and an organizer updates the
// attachment.
func (u *ExerciseUseCase) importUsage(
	ctx context.Context,
	target exerciseModel.Exercise,
	result exerciseModel.ExerciseVersion,
) (exerciseModel.ImportUsage, error) {
	usage := exerciseModel.ImportUsage{Events: []exerciseModel.ImportUsageEvent{}}
	now := u.timeNow()
	attachments, err := u.exercises.ActiveAttachments(ctx, target.ID, now)
	if err != nil {
		return usage, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list exercise attachments").
			Err()
	}
	if usage.TestDeploys, err = u.exercises.LeasedTestDeploys(ctx, target.ID, now); err != nil {
		return usage, model.ErrPlatform.WithError(err).
			WithMessage("Failed to count test deploys").
			Err()
	}
	if len(attachments) == 0 {
		return usage, nil
	}
	counts, err := u.exercises.AnswerCounts(ctx, target.ID)
	if err != nil {
		return usage, model.ErrPlatform.WithError(err).
			WithMessage("Failed to read recorded answers").
			Err()
	}
	pinned := map[uuid.UUID]exerciseModel.ExerciseVersion{}
	for _, attachment := range attachments {
		version, known := pinned[attachment.ExerciseVersionID]
		if !known {
			if version, err = u.exercises.GetVersion(
				ctx,
				attachment.ExerciseVersionID,
			); err != nil {
				return usage, model.ErrPlatform.WithError(err).
					WithMessage("Failed to read pinned version").
					Err()
			}
			pinned[attachment.ExerciseVersionID] = version
		}
		usage.Events = append(
			usage.Events,
			usageOfAttachment(attachment, version, target.PublishedVersionID, result, counts),
		)
	}
	return usage, nil
}

func usageOfAttachment(
	attachment exerciseRepo.ActiveAttachment,
	pinned exerciseModel.ExerciseVersion,
	published uuid.NullUUID,
	result exerciseModel.ExerciseVersion,
	counts map[[2]uuid.UUID]exerciseRepo.AnswerCount,
) exerciseModel.ImportUsageEvent {
	event := exerciseModel.ImportUsageEvent{
		EventID:          attachment.EventID,
		Name:             attachment.EventName,
		Archived:         attachment.Archived,
		VersionPublished: published.Valid && published.UUID == attachment.ExerciseVersionID,
		RunningLabs:      attachment.RunningLabs,
		Removed:          []exerciseModel.ImportUsageTask{},
		Added:            []exerciseModel.ImportUsageTask{},
	}
	index := 0
	if attachment.FixedVariantIndex != nil {
		index = int(*attachment.FixedVariantIndex)
	}
	if index >= len(result.Variants) {
		event.VariantMissing, event.Blocked = true, true
		return event
	}
	var old []exerciseModel.Task
	if index < len(pinned.Variants) {
		old = pinned.Variants[index].Tasks
	}
	added, removed := eventTaskChange(old, result.Variants[index].Tasks)
	for _, task := range added {
		event.Added = append(event.Added, exerciseModel.ImportUsageTask{Name: task.Name})
	}
	for _, task := range removed {
		count := counts[[2]uuid.UUID{attachment.ID, task.ID}]
		event.Removed = append(
			event.Removed,
			exerciseModel.ImportUsageTask{
				Name:     task.Name,
				Attempts: count.Attempts,
				Solves:   count.Solves,
			},
		)
		if count.Attempts > 0 {
			event.Blocked = true
		}
	}
	return event
}

// eventTaskChange mirrors the pairing of the event's board with a new version (the event use case's
// matchBoardTasks): by task id, then, for the rest, by name.
func eventTaskChange(old, next []exerciseModel.Task) ([]exerciseModel.Task, []exerciseModel.Task) {
	var added, removed []exerciseModel.Task
	taken := make(map[uuid.UUID]bool, len(next))
	byID := make(map[uuid.UUID]bool, len(next))
	for _, task := range next {
		byID[task.ID] = true
	}
	var unmatched []exerciseModel.Task
	for _, task := range old {
		if byID[task.ID] {
			taken[task.ID] = true
		} else {
			unmatched = append(unmatched, task)
		}
	}
	freeByName := map[string][]exerciseModel.Task{}
	for _, task := range next {
		if !taken[task.ID] {
			name := strings.ToLower(strings.TrimSpace(task.Name))
			freeByName[name] = append(freeByName[name], task)
		}
	}
	for _, task := range unmatched {
		name := strings.ToLower(strings.TrimSpace(task.Name))
		candidates := freeByName[name]
		if name == "" || len(candidates) == 0 {
			removed = append(removed, task)
			continue
		}
		taken[candidates[0].ID] = true
		freeByName[name] = candidates[1:]
	}
	for _, task := range next {
		if !taken[task.ID] {
			added = append(added, task)
		}
	}
	return added, removed
}
