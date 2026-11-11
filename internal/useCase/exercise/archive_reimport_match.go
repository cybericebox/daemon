package exercise

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Identity matching of an import into an existing exercise.
//
// The import replaces the draft with the document, no field is merged. What is kept from the stored working copy
// is identity, so that what points at it (event challenges and their recorded answers by task id, the checks of the
// laboratory by task key, laboratory data by device id) stays valid:
//
//   - variants: by id; the rest in order (the n-th unmatched variant of the document with the n-th unmatched stored
//     one). A variant has no name and its index is decorative, so id and then order are all there is;
//   - tasks of a variant: by id, then by key, against the tasks of the matched stored variant (a variant without a
//     match looks at the first stored variant, which carries the identity of the task in every variant);
//   - devices of a variant: by name only. A renamed device is a removed one and a new one;
//   - hints of a matched task: by id, then by position.
//
// A matched task keeps its stored id and its stored key. The references to a key the document used (requires,
// {{task.<key>}}) are rewritten to the stored key. Items the document lacks are not in the draft any more. Items
// the stored copy lacks are new and get their ids when the draft is written.

var taskRefRe = regexp.MustCompile(`\{\{task\.([a-z][a-z0-9_]{0,31})\}\}`)

// matchStoredIdentity pairs the version with the stored working copy, rewrites the ids and keys of the version to
// the stored ones and reports the pairing. The version must be prepared (ids filled in, links synced, keys set).
func matchStoredIdentity(
	in *exerciseModel.ExerciseVersion,
	stored exerciseModel.ExerciseVersion,
) (exerciseModel.ImportDiff, []exerciseModel.ImportWarning, error) {
	ids := map[uuid.UUID]uuid.UUID{}
	var warnings []exerciseModel.ImportWarning
	diff := exerciseModel.ImportDiff{Variants: []exerciseModel.ImportVariantDiff{}}

	inToStored := matchVariants(in.Variants, stored.Variants)
	storedClaimed := make([]bool, len(stored.Variants))
	for _, j := range inToStored.pairs {
		if j >= 0 {
			storedClaimed[j] = true
		}
	}
	storedTaskIDs := map[uuid.UUID]bool{}
	storedDeviceIDs := map[uuid.UUID]bool{}
	for _, variant := range stored.Variants {
		for _, task := range variant.Tasks {
			storedTaskIDs[task.ID] = true
		}
		for _, device := range variant.Topology.Devices {
			storedDeviceIDs[device.ID] = true
		}
	}
	keyRenames := make([]map[string]string, len(in.Variants))

	for i := range in.Variants {
		variant := &in.Variants[i]
		entry := exerciseModel.ImportVariantDiff{
			Position: i + 1,
			Tasks:    emptyChanges(),
			Devices:  emptyChanges(),
		}
		var storedVariant *exerciseModel.Variant
		if j := inToStored.pairs[i]; j >= 0 {
			storedVariant = &stored.Variants[j]
			entry.Status, entry.StoredPosition, entry.MatchedBy = exerciseModel.ImportVariantKept, j+1, inToStored.by[i]
			if variant.ID != uuid.Nil && variant.ID != storedVariant.ID {
				ids[variant.ID] = storedVariant.ID
			}
		} else {
			entry.Status = exerciseModel.ImportVariantAdded
		}

		pool := []exerciseModel.Task(nil)
		switch {
		case storedVariant != nil:
			pool = storedVariant.Tasks
		case len(stored.Variants) > 0:
			pool = stored.Variants[0].Tasks
		}
		var conflicts []exerciseModel.ImportWarning
		keyRenames[i], conflicts = matchTasks(
			variant,
			pool,
			ids,
			storedTaskIDs,
			&entry.Tasks,
			storedVariant != nil,
		)
		warnings = append(warnings, conflicts...)

		if storedVariant != nil {
			matchDevices(variant, storedVariant, ids, storedDeviceIDs, &entry.Devices)
		} else {
			for _, device := range variant.Topology.Devices {
				entry.Devices.Added = append(
					entry.Devices.Added,
					exerciseModel.ImportItem{Name: device.Name},
				)
				if storedDeviceIDs[device.ID] {
					ids[device.ID] = uuid.Must(uuid.NewV7())
				}
			}
		}
		diff.Variants = append(diff.Variants, entry)
	}
	for j, claimed := range storedClaimed {
		if claimed {
			continue
		}
		removed := exerciseModel.ImportVariantDiff{
			Status:         exerciseModel.ImportVariantRemoved,
			StoredPosition: j + 1,
			Tasks:          emptyChanges(),
			Devices:        emptyChanges(),
		}
		for _, task := range stored.Variants[j].Tasks {
			removed.Tasks.Removed = append(
				removed.Tasks.Removed,
				exerciseModel.ImportItem{Name: task.Name, Key: task.Key, ID: task.ID},
			)
		}
		for _, device := range stored.Variants[j].Topology.Devices {
			removed.Devices.Removed = append(
				removed.Devices.Removed,
				exerciseModel.ImportItem{Name: device.Name, ID: device.ID},
			)
		}
		diff.Variants = append(diff.Variants, removed)
	}
	if err := applyIdentity(in, ids, keyRenames); err != nil {
		return diff, warnings, err
	}
	return diff, warnings, nil
}

func emptyChanges() exerciseModel.ImportChanges {
	return exerciseModel.ImportChanges{
		Kept:    []exerciseModel.ImportItem{},
		Added:   []exerciseModel.ImportItem{},
		Removed: []exerciseModel.ImportItem{},
	}
}

type variantPairs struct {
	pairs []int // incoming position -> stored position, -1 for none
	by    []exerciseModel.ImportMatch
}

// matchVariants pairs the variants by id, then the rest in order.
func matchVariants(incoming, stored []exerciseModel.Variant) variantPairs {
	out := variantPairs{
		pairs: make([]int, len(incoming)),
		by:    make([]exerciseModel.ImportMatch, len(incoming)),
	}
	claimed := make([]bool, len(stored))
	for i := range out.pairs {
		out.pairs[i] = -1
	}
	for i, variant := range incoming {
		if variant.ID == uuid.Nil {
			continue
		}
		for j, candidate := range stored {
			if !claimed[j] && candidate.ID == variant.ID {
				out.pairs[i], out.by[i], claimed[j] = j, exerciseModel.ImportMatchID, true
				break
			}
		}
	}
	next := 0
	for i := range incoming {
		if out.pairs[i] >= 0 {
			continue
		}
		for next < len(stored) && claimed[next] {
			next++
		}
		if next < len(stored) {
			out.pairs[i], out.by[i], claimed[next] = next, exerciseModel.ImportMatchOrder, true
		}
	}
	return out
}

// matchTasks pairs the tasks of the variant with the pool by id, then by key, records the ids to rewrite and the
// key renames, pairs the hints and fills the report. report tells whether the variant itself is a kept one: only
// then the unmatched tasks of the pool are reported as removed.
func matchTasks(
	variant *exerciseModel.Variant,
	pool []exerciseModel.Task,
	ids map[uuid.UUID]uuid.UUID,
	storedTaskIDs map[uuid.UUID]bool,
	changes *exerciseModel.ImportChanges,
	kept bool,
) (map[string]string, []exerciseModel.ImportWarning) {
	claimed := make([]bool, len(pool))
	pair := make([]int, len(variant.Tasks))
	by := make([]exerciseModel.ImportMatch, len(variant.Tasks))
	for i := range pair {
		pair[i] = -1
	}
	for i, task := range variant.Tasks {
		want := task.ID
		if mapped, ok := ids[task.ID]; ok {
			want = mapped
		}
		if want == uuid.Nil {
			continue
		}
		for j, candidate := range pool {
			if !claimed[j] && candidate.ID == want {
				pair[i], by[i], claimed[j] = j, exerciseModel.ImportMatchID, true
				break
			}
		}
	}
	for i, task := range variant.Tasks {
		if pair[i] >= 0 || task.Key == "" {
			continue
		}
		for j, candidate := range pool {
			if !claimed[j] && candidate.Key == task.Key {
				pair[i], by[i], claimed[j] = j, exerciseModel.ImportMatchKey, true
				break
			}
		}
	}
	// The keys the variant ends up with: a stored key is taken unless another task of the document holds it.
	final := make(map[string]int, len(variant.Tasks))
	for i, task := range variant.Tasks {
		final[task.Key] = i
	}
	renames := map[string]string{}
	var warnings []exerciseModel.ImportWarning
	conflict := map[int]bool{}
	for i, task := range variant.Tasks {
		if pair[i] < 0 || pool[pair[i]].Key == task.Key {
			continue
		}
		stored := pool[pair[i]].Key
		if owner, taken := final[stored]; taken && owner != i {
			conflict[i] = true
			warnings = append(
				warnings,
				exerciseModel.ImportWarning{
					Code:    exerciseModel.ImportWarnKeyConflict,
					Context: map[string]any{"task": task.Name, "key": stored},
				},
			)
			continue
		}
		renames[task.Key] = stored
	}
	for i, task := range variant.Tasks {
		item := exerciseModel.ImportItem{Name: task.Name, Key: task.Key}
		if pair[i] < 0 {
			if storedTaskIDs[task.ID] {
				if _, mapped := ids[task.ID]; !mapped {
					ids[task.ID] = uuid.Must(uuid.NewV7())
				}
			}
			changes.Added = append(changes.Added, item)
			continue
		}
		stored := pool[pair[i]]
		if stored.ID != task.ID {
			if _, mapped := ids[task.ID]; !mapped {
				ids[task.ID] = stored.ID
			}
		}
		item.ID, item.MatchedBy, item.Key = stored.ID, by[i], stored.Key
		if conflict[i] {
			item.Key, item.KeyChanged = task.Key, true
		}
		if stored.Name != task.Name {
			item.PreviousName = stored.Name
		}
		changes.Kept = append(changes.Kept, item)
		matchHints(&variant.Tasks[i], stored, ids)
	}
	if kept {
		for j, candidate := range pool {
			if !claimed[j] {
				changes.Removed = append(
					changes.Removed,
					exerciseModel.ImportItem{
						Name: candidate.Name,
						Key:  candidate.Key,
						ID:   candidate.ID,
					},
				)
			}
		}
	}
	return renames, warnings
}

// matchHints pairs the hints of a matched task by id, then by position.
func matchHints(task *exerciseModel.Task, stored exerciseModel.Task, ids map[uuid.UUID]uuid.UUID) {
	claimed := make([]bool, len(stored.Hints))
	paired := make([]bool, len(task.Hints))
	for i, hint := range task.Hints {
		for j, candidate := range stored.Hints {
			if !claimed[j] && hint.ID != uuid.Nil && candidate.ID == hint.ID {
				claimed[j], paired[i] = true, true
				break
			}
		}
	}
	for i, hint := range task.Hints {
		if paired[i] || i >= len(stored.Hints) || claimed[i] {
			continue
		}
		claimed[i] = true
		if _, mapped := ids[hint.ID]; !mapped && hint.ID != stored.Hints[i].ID {
			ids[hint.ID] = stored.Hints[i].ID
		}
	}
}

// matchDevices pairs the devices of the variant with those of the stored variant by name.
func matchDevices(
	variant *exerciseModel.Variant,
	stored *exerciseModel.Variant,
	ids map[uuid.UUID]uuid.UUID,
	storedDeviceIDs map[uuid.UUID]bool,
	changes *exerciseModel.ImportChanges,
) {
	claimed := make([]bool, len(stored.Topology.Devices))
	for _, device := range variant.Topology.Devices {
		found := -1
		for j, candidate := range stored.Topology.Devices {
			if !claimed[j] && candidate.Name == device.Name {
				found = j
				break
			}
		}
		item := exerciseModel.ImportItem{Name: device.Name}
		if found < 0 {
			if storedDeviceIDs[device.ID] {
				if _, mapped := ids[device.ID]; !mapped {
					ids[device.ID] = uuid.Must(uuid.NewV7())
				}
			}
			changes.Added = append(changes.Added, item)
			continue
		}
		claimed[found] = true
		kept := stored.Topology.Devices[found]
		if kept.ID != device.ID {
			if _, mapped := ids[device.ID]; !mapped {
				ids[device.ID] = kept.ID
			}
		}
		item.ID, item.MatchedBy = kept.ID, exerciseModel.ImportMatchName
		changes.Kept = append(changes.Kept, item)
	}
	for j, candidate := range stored.Topology.Devices {
		if !claimed[j] {
			changes.Removed = append(
				changes.Removed,
				exerciseModel.ImportItem{Name: candidate.Name, ID: candidate.ID},
			)
		}
	}
}

// applyIdentity writes the ids and keys the pairing decided into the version. Ids are replaced everywhere they
// occur (links, endpoints, conditions, image overrides, the layout) in one pass through two stages, so that two
// items that swap ids do not meet halfway.
func applyIdentity(
	in *exerciseModel.ExerciseVersion,
	ids map[uuid.UUID]uuid.UUID,
	keyRenames []map[string]string,
) error {
	for i := range in.Variants {
		renames := keyRenames[i]
		if len(renames) == 0 {
			continue
		}
		variant := &in.Variants[i]
		for ti := range variant.Tasks {
			task := &variant.Tasks[ti]
			if to, ok := renames[task.Key]; ok {
				task.Key = to
			}
			for ri, key := range task.Requires {
				if to, ok := renames[key]; ok {
					task.Requires[ri] = to
				}
			}
		}
		if err := rewriteTaskRefs(variant, renames); err != nil {
			return err
		}
	}
	changed := make([]uuid.UUID, 0, len(ids))
	for from, to := range ids {
		if from != to {
			changed = append(changed, from)
		}
	}
	if len(changed) == 0 {
		return nil
	}
	encoded, err := json.Marshal(in.Variants)
	if err != nil {
		return err
	}
	text := string(encoded)
	for n, from := range changed {
		text = strings.ReplaceAll(text, from.String(), fmt.Sprintf("@@identity-%d@@", n))
	}
	for n, from := range changed {
		text = strings.ReplaceAll(text, fmt.Sprintf("@@identity-%d@@", n), ids[from].String())
	}
	var variants []exerciseModel.Variant
	if err = json.Unmarshal([]byte(text), &variants); err != nil {
		return err
	}
	in.Variants = variants
	return nil
}

// rewriteTaskRefs renames {{task.<key>}} references inside one variant (every string of it).
func rewriteTaskRefs(variant *exerciseModel.Variant, renames map[string]string) error {
	encoded, err := json.Marshal(variant)
	if err != nil {
		return err
	}
	rewritten := taskRefRe.ReplaceAllStringFunc(string(encoded), func(match string) string {
		key := taskRefRe.FindStringSubmatch(match)[1]
		if to, ok := renames[key]; ok {
			return "{{task." + to + "}}"
		}
		return match
	})
	if rewritten == string(encoded) {
		return nil
	}
	var out exerciseModel.Variant
	if err = json.Unmarshal([]byte(rewritten), &out); err != nil {
		return err
	}
	*variant = out
	return nil
}
