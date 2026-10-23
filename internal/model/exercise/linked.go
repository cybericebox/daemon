package exerciseModel

import (
	"encoding/json"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// IsLinked reports whether the variant inherits its content from a base
// variant of the same version.
func (v Variant) IsLinked() bool { return v.LinkedTo.Valid }

// SyncLinkedVariants rebuilds every linked variant from its base. A link to a
// variant that is not in the version is dropped (the content stays as an
// independent variant); a link to itself or to a linked variant is an error.
// A linked variant keeps only its ID, Index, Note, link and, per task, its own
// Flag and Attachments (matched by task ID, else by position); everything else
// is a deep copy of the base. Idempotent.
func (v *ExerciseVersion) SyncLinkedVariants() error {
	positionByID := make(map[uuid.UUID]int, len(v.Variants))
	for i, variant := range v.Variants {
		if _, seen := positionByID[variant.ID]; !seen {
			positionByID[variant.ID] = i
		}
	}
	for i := range v.Variants {
		if v.Variants[i].LinkedTo.Valid {
			if _, ok := positionByID[v.Variants[i].LinkedTo.UUID]; !ok {
				v.Variants[i].LinkedTo = uuid.NullUUID{}
			}
		}
	}
	for i, variant := range v.Variants {
		if !variant.LinkedTo.Valid {
			continue
		}
		basePosition := positionByID[variant.LinkedTo.UUID]
		if basePosition == i || v.Variants[basePosition].LinkedTo.Valid {
			return ErrVariantLinkInvalid.WithContext("variant", i+1).Err()
		}
	}
	for i := range v.Variants {
		linked := v.Variants[i]
		if !linked.LinkedTo.Valid {
			continue
		}
		rebuilt, err := deepCopyVariant(v.Variants[positionByID[linked.LinkedTo.UUID]])
		if err != nil {
			return err
		}
		for taskIndex := range rebuilt.Tasks {
			rebuilt.Tasks[taskIndex].Flag = nil
			rebuilt.Tasks[taskIndex].Attachments = nil
			if own, ok := matchLinkedTask(linked.Tasks, rebuilt.Tasks[taskIndex].ID, taskIndex); ok {
				rebuilt.Tasks[taskIndex].Flag = own.Flag
				rebuilt.Tasks[taskIndex].Attachments = own.Attachments
			}
		}
		rebuilt.ID, rebuilt.Index, rebuilt.Note, rebuilt.LinkedTo, rebuilt.Weight = linked.ID, linked.Index, linked.Note, linked.LinkedTo, linked.Weight
		v.Variants[i] = rebuilt
	}
	return nil
}

// deepCopyVariant copies through JSON, so fields tagged json:"-" are dropped
// (Variant has none today).
func deepCopyVariant(variant Variant) (Variant, error) {
	data, err := json.Marshal(variant)
	if err != nil {
		return Variant{}, err
	}
	var out Variant
	if err = json.Unmarshal(data, &out); err != nil {
		return Variant{}, err
	}
	return out, nil
}

// matchLinkedTask finds the linked variant's own task for a base task: by task
// ID when both IDs are non-nil, else by position (a linked task that has no ID
// takes the task at the same position).
func matchLinkedTask(tasks []Task, baseID uuid.UUID, position int) (Task, bool) {
	if baseID != uuid.Nil {
		for _, task := range tasks {
			if task.ID == baseID {
				return task, true
			}
		}
	}
	if position < len(tasks) && (baseID == uuid.Nil || tasks[position].ID == uuid.Nil) {
		return tasks[position], true
	}
	return Task{}, false
}

// validateLinkedFlags: within a base and its linked variants, a fixed flag
// value of one task position must not repeat across variants (templates may).
func validateLinkedFlags(variants []Variant) error {
	groups := make(map[uuid.UUID][]Variant, len(variants))
	for _, variant := range variants {
		if variant.LinkedTo.Valid {
			groups[variant.LinkedTo.UUID] = append(groups[variant.LinkedTo.UUID], variant)
		}
	}
	for _, base := range variants {
		linked := groups[base.ID]
		if base.LinkedTo.Valid || len(linked) == 0 {
			continue
		}
		group := append([]Variant{base}, linked...)
		for taskIndex, task := range base.Tasks {
			owner := make(map[string]int)
			for variantIndex, member := range group {
				if taskIndex >= len(member.Tasks) {
					continue
				}
				for _, value := range member.Tasks[taskIndex].Flag {
					if strings.HasPrefix(value, flagpattern.TemplatePrefix) {
						continue
					}
					if previous, seen := owner[value]; seen && previous != variantIndex {
						return ErrLinkedFlagsNotDistinct.WithContext("task", task.Name).Err()
					}
					owner[value] = variantIndex
				}
			}
		}
	}
	return nil
}
