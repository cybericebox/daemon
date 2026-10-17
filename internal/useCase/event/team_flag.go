package event

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"slices"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/flagFilterRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/flagfilter"
	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// teamFlags issues the expected flags of event teams for one event exercise in
// one transaction. A task with candidates gets a crypto-random value of its
// candidates that its filter (per event exercise, variant and subtask) has not
// seen; the filter is read and written under the event exercise's variant pick
// lock, in the transaction that stores the team challenge. The moderators team
// draws through the filter like any team. A task without candidates keeps the
// full random mode and touches no filter.
type teamFlags struct {
	repo            IRepository
	eventID         uuid.UUID
	eventExerciseID uuid.UUID
	randomBytes     int
	source          io.Reader
	locked          bool
	capacity        int
	filters         map[flagFilterRepo.Key]*flagfilter.Filter
}

func (u *EventUseCase) newTeamFlags(repo IRepository, eventID, eventExerciseID uuid.UUID) *teamFlags {
	return &teamFlags{repo: repo, eventID: eventID, eventExerciseID: eventExerciseID, randomBytes: u.flagRandomBytes, source: rand.Reader,
		filters: map[flagFilterRepo.Key]*flagfilter.Filter{}}
}

// heldLock records that the transaction already holds the variant pick lock.
func (f *teamFlags) heldLock() {
	if f != nil {
		f.locked = true
	}
}

func (f *teamFlags) lock(ctx context.Context) error {
	if f.locked {
		return nil
	}
	if err := teamChallengeRepo.New(f.repo).LockVariantPick(ctx, f.eventExerciseID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to lock the variant pick").Err()
	}
	f.locked = true
	return nil
}

func (f *teamFlags) teamCapacity(ctx context.Context) (int, error) {
	if f.capacity > 0 {
		return f.capacity, nil
	}
	config, err := eventConfigRepo.New(f.repo).Get(ctx, f.eventID)
	if err != nil {
		return 0, model.ErrPlatform.WithError(err).WithMessage("Failed to get the event team limit").Err()
	}
	f.capacity = flagfilter.Capacity(config.MaxTeams)
	return f.capacity, nil
}

// issue is a team's expected flag for one task of a variant.
func (f *teamFlags) issue(ctx context.Context, variantIndex int32, task exerciseModel.Task) (string, error) {
	if len(task.Flag) == 0 {
		return flagpattern.Resolve(nil, f.randomBytes, f.source)
	}
	set, err := flagpattern.ParseSet(task.Flag)
	if err != nil {
		return "", err
	}
	if err = f.lock(ctx); err != nil {
		return "", err
	}
	capacity, err := f.teamCapacity(ctx)
	if err != nil {
		return "", err
	}
	key := flagFilterRepo.Key{EventExerciseID: f.eventExerciseID, VariantIndex: variantIndex, TaskID: task.ID}
	filter, err := f.filter(ctx, key, set, capacity)
	if err != nil {
		return "", err
	}
	if filter.Full() {
		// Grow before the next issue: the flags issued so far in this
		// transaction are already stored, so the rebuild sees them.
		if filter, err = f.rebuild(ctx, key, set, filter.Capacity*2, filter.Resets); err != nil {
			return "", err
		}
	}
	flag, err := filter.Issue(set, f.source)
	if err != nil {
		return "", err
	}
	if err = flagFilterRepo.New(f.repo).Save(ctx, key, filter); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to save the flag filter").Err()
	}
	f.filters[key] = filter
	return flag, nil
}

// filter is the key's filter: the stored one while it fits the set and the
// capacity, else rebuilt from the stored flags of the key.
func (f *teamFlags) filter(ctx context.Context, key flagFilterRepo.Key, set flagpattern.Set, capacity int) (*flagfilter.Filter, error) {
	filter, cached := f.filters[key]
	if !cached {
		stored, err := flagFilterRepo.New(f.repo).Get(ctx, key)
		if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get the flag filter").Err()
		}
		filter = stored
	}
	if filter.Fits(set, capacity) {
		return filter, nil
	}
	resets := 0
	if filter != nil {
		capacity, resets = max(capacity, filter.Capacity), filter.Resets
	}
	return f.rebuild(ctx, key, set, capacity, resets)
}

func (f *teamFlags) rebuild(ctx context.Context, key flagFilterRepo.Key, set flagpattern.Set, capacity, resets int) (*flagfilter.Filter, error) {
	issued, err := flagFilterRepo.New(f.repo).Issued(ctx, key)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read the issued flags").Err()
	}
	filter, err := flagfilter.New(set, capacity, issued, f.source)
	if err != nil {
		return nil, err
	}
	filter.Resets = resets
	return filter, nil
}

// rebuildEventFlagFilters rewrites every flag filter of an event at the team
// capacity of maxTeams (at least the flags a filter already holds), with new
// salts, in the caller's transaction: it takes the variant pick lock of every
// event exercise of the event in id order, reads the stored flags of the event
// in one pass and writes each filter that exists or has issued flags. It
// rebuilds from stored state only, so a retry is safe.
func (u *EventUseCase) rebuildEventFlagFilters(ctx context.Context, repo IRepository, eventID uuid.UUID, maxTeams *int32) error {
	attachments, err := eventExerciseRepo.New(repo).List(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event exercises").Err()
	}
	slices.SortFunc(attachments, func(a, b eventExerciseModel.EventExercise) int { return bytes.Compare(a.ID.Bytes(), b.ID.Bytes()) })
	teamChallenges := teamChallengeRepo.New(repo)
	versionIDs := make([]uuid.UUID, 0, len(attachments))
	for _, attachment := range attachments {
		if err = teamChallenges.LockVariantPick(ctx, attachment.ID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to lock the variant pick").Err()
		}
		versionIDs = append(versionIDs, attachment.ExerciseVersionID)
	}
	filters := flagFilterRepo.New(repo)
	existing, err := filters.Resets(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list the flag filters").Err()
	}
	issued, err := filters.EventIssued(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read the issued flags").Err()
	}
	candidates, err := filters.VersionTaskFlags(ctx, versionIDs)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read the task flags").Err()
	}
	capacity := flagfilter.Capacity(maxTeams)
	for _, attachment := range attachments {
		for variant, tasks := range candidates[attachment.ExerciseVersionID] {
			for taskID, flags := range tasks {
				key := flagFilterRepo.Key{EventExerciseID: attachment.ID, VariantIndex: int32(variant), TaskID: taskID}
				resets, has := existing[key]
				if len(flags) == 0 || (!has && len(issued[key]) == 0) {
					continue
				}
				set, parseErr := flagpattern.ParseSet(flags)
				if parseErr != nil {
					log.Warn().Err(parseErr).Stringer("eventExerciseID", attachment.ID).Msg("Skipped the flag filter of an invalid candidate list")
					continue
				}
				filter, newErr := flagfilter.New(set, capacity, issued[key], rand.Reader)
				if newErr != nil {
					return newErr
				}
				filter.Resets = resets
				if err = filters.Save(ctx, key, filter); err != nil {
					return model.ErrPlatform.WithError(err).WithMessage("Failed to save the flag filter").Err()
				}
			}
		}
	}
	return nil
}

// decorateFlagWarnings marks the attachments whose flags may repeat: a task
// variant with candidates has fewer outputs than the team capacity, or its
// filter was reset. It is advisory: a failed read only logs.
func (u *EventUseCase) decorateFlagWarnings(ctx context.Context, eventID uuid.UUID, items []EventExerciseView) {
	if len(items) == 0 || u.repo == nil {
		return
	}
	err := func() error {
		config, err := u.configs.Get(ctx, eventID)
		if err != nil {
			return err
		}
		capacity := flagfilter.Capacity(config.MaxTeams)
		filters := flagFilterRepo.New(u.repo)
		resets, err := filters.Resets(ctx, eventID)
		if err != nil {
			return err
		}
		versionIDs := make([]uuid.UUID, 0, len(items))
		for _, item := range items {
			versionIDs = append(versionIDs, item.ExerciseVersionID)
		}
		candidates, err := filters.VersionTaskFlags(ctx, versionIDs)
		if err != nil {
			return err
		}
		for i := range items {
			items[i].FlagsMayRepeat = flagsMayRepeat(items[i].ID, candidates[items[i].ExerciseVersionID], capacity, resets)
		}
		return nil
	}()
	if err != nil {
		log.Warn().Err(err).Stringer("eventID", eventID).Msg("Failed to read the flag repeat warnings")
	}
}

func flagsMayRepeat(eventExerciseID uuid.UUID, variants []map[uuid.UUID][]string, capacity int, resets map[flagFilterRepo.Key]int) bool {
	for variant, tasks := range variants {
		for taskID, flags := range tasks {
			if len(flags) == 0 {
				continue
			}
			if resets[flagFilterRepo.Key{EventExerciseID: eventExerciseID, VariantIndex: int32(variant), TaskID: taskID}] > 0 {
				return true
			}
			if set, err := flagpattern.ParseSet(flags); err == nil && flagfilter.SmallSet(set, capacity) {
				return true
			}
		}
	}
	return false
}
