package exerciseModel

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
	"github.com/gofrs/uuid"
)

type VersionStatus string

const (
	VersionStatusDraft       VersionStatus = "draft"
	VersionStatusPublished   VersionStatus = "published"
	VersionStatusUnpublished VersionStatus = "unpublished"
	VersionStatusCheckpoint  VersionStatus = "checkpoint"
)

func (s VersionStatus) Valid() bool {
	return s.IsDraft() || s.IsPublished() || s.IsUnpublished() || s.IsCheckpoint()
}
func (s VersionStatus) IsDraft() bool       { return s == VersionStatusDraft }
func (s VersionStatus) IsPublished() bool   { return s == VersionStatusPublished }
func (s VersionStatus) IsUnpublished() bool { return s == VersionStatusUnpublished }
func (s VersionStatus) IsCheckpoint() bool  { return s == VersionStatusCheckpoint }

type Difficulty string

const (
	DifficultyElementary Difficulty = "elementary"
	DifficultyTrivial    Difficulty = "trivial"
	DifficultyEasy       Difficulty = "easy"
	DifficultyMedium     Difficulty = "medium"
	DifficultyHard       Difficulty = "hard"
	DifficultyInsane     Difficulty = "insane"
)

func (d Difficulty) Valid() bool {
	switch d {
	case DifficultyElementary, DifficultyTrivial, DifficultyEasy, DifficultyMedium, DifficultyHard, DifficultyInsane:
		return true
	}
	return false
}

type AttachmentRef struct {
	FileID uuid.UUID `json:"file_id"`
	Name   string    `json:"name"`
}

// Task is one subtask of a variant. Description is a block document (JSON)
// rendered by the frontend with placeholders substituted at deploy time.
type Task struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Description json.RawMessage `json:"description,omitempty"`
	Difficulty  Difficulty      `json:"difficulty"`

	// Flag holds the candidate flag values: len 0 → a flag is generated
	// randomly at deploy time; len 1 → that fixed value is used as-is; len >1
	// → one value is picked at random at deploy time.
	Flag []string `json:"flag,omitempty"`
	// LinkedDeviceID + DeviceFlagVar: the flag is injected into that device's
	// env var at deploy time.
	LinkedDeviceID uuid.NullUUID `json:"linked_device_id,omitempty"`
	DeviceFlagVar  string        `json:"device_flag_var,omitempty"`

	Attachments  []AttachmentRef `json:"attachments,omitempty"`
	Placeholders []Placeholder   `json:"placeholders,omitempty"`
	// Hints a team may unlock. IDs and levels match position-wise across
	// variants (like task IDs); the text may differ per variant.
	Hints []Hint `json:"hints,omitempty"`
}

// The hint text is a serialized rich-text document (the editor's JSON, like
// a task description); plain text saved before formatting still reads.
const (
	hintsMaxCount  = 10
	hintTextMaxLen = 20000
)

// HintLevel is how much a hint helps solve the task. The catalog carries no
// price: an event sets the cost of each hint on its board.
type HintLevel string

const (
	HintLevelNudge        HintLevel = "nudge"         // points where to look
	HintLevelDirection    HintLevel = "direction"     // names the approach or tool
	HintLevelSteps        HintLevel = "steps"         // gives the steps
	HintLevelNearSolution HintLevel = "near_solution" // only execution remains
)

func (l HintLevel) Valid() bool {
	switch l {
	case HintLevelNudge, HintLevelDirection, HintLevelSteps, HintLevelNearSolution:
		return true
	default:
		return false
	}
}

// Hint is one unlockable hint of a task. Version JSON saved before levels
// carried a "cost" field; it is ignored on decode and a missing level reads
// as nudge.
type Hint struct {
	ID    uuid.UUID `json:"id"`
	Text  string    `json:"text"`
	Level HintLevel `json:"level"`
}

// UnmarshalJSON defaults a missing level to nudge (hints saved before levels).
func (h *Hint) UnmarshalJSON(data []byte) error {
	type plain Hint
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Level == "" {
		value.Level = HintLevelNudge
	}
	*h = Hint(value)
	return nil
}

// hasHintTextContent: a hint that exists must say something. A rich-text
// document counts only with non-blank text (or a placeholder); plain text
// from older drafts only when not blank.
func hasHintTextContent(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	var document struct {
		Root json.RawMessage `json:"root"`
	}
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &document) == nil && len(document.Root) > 0 {
		return hasTaskDescriptionContent(document.Root)
	}
	return true
}

// validateHints checks one task's hint list against the canonical (variant 0)
// task: same count, IDs and levels at the same positions.
func validateHints(task, canonical Task) error {
	if len(task.Hints) > hintsMaxCount || len(task.Hints) != len(canonical.Hints) {
		return ErrHintsInvalid.WithContext("task", task.Name).Err()
	}
	seen := make(map[uuid.UUID]struct{}, len(task.Hints))
	for i, hint := range task.Hints {
		if !hint.Level.Valid() || len(hint.Text) > hintTextMaxLen ||
			hint.ID != canonical.Hints[i].ID || hint.Level != canonical.Hints[i].Level {
			return ErrHintsInvalid.WithContext("task", task.Name).Err()
		}
		if _, duplicate := seen[hint.ID]; duplicate && hint.ID != uuid.Nil {
			return ErrHintsInvalid.WithContext("task", task.Name).Err()
		}
		seen[hint.ID] = struct{}{}
	}
	return nil
}

type Variant struct {
	ID       uuid.UUID `json:"id"`
	Index    int32     `json:"index"` // decorative admin-facing number; not an identity
	Note     string    `json:"note"`  // per-variant admin note (never shown to participants)
	Tasks    []Task    `json:"tasks"`
	Topology Topology  `json:"topology"`
}

// ValidateTopologyForDeploy enforces runnable graph rules without requiring
// publication-only task descriptions or flag content in a saved draft.
func (v Variant) ValidateTopologyForDeploy() error {
	if err := v.Topology.validateStructure(); err != nil {
		return err
	}
	return v.Topology.validateGraph(v.Tasks)
}

// ExerciseVersion is a full content snapshot: the variant set persisted as one
// JSONB blob. Status transitions are owned by the lifecycle CTE queries.
type ExerciseVersion struct {
	ID         uuid.UUID
	ExerciseID uuid.UUID
	Status     VersionStatus
	AdminNote  string
	// Label names an explicit snapshot (checkpoint note); '' for every other
	// version. Not content: excluded from HasChanges, never restored into the
	// working copy. Part of the portable archive format.
	Label string

	Variants []Variant

	CreatedAt   time.Time
	CreatedBy   uuid.NullUUID
	PublishedAt *time.Time
}

// validFlag reports whether a task's flag candidates are well-formed: an
// empty/nil slice is valid (flag generated randomly at deploy time), any
// supplied value must use the ICE{...} format without whitespace or controls.
func validFlag(flag []string) bool {
	seen := make(map[string]struct{}, len(flag))
	for _, v := range flag {
		if _, err := flagpattern.Parse(v); err != nil {
			return false
		}
		if _, duplicate := seen[v]; duplicate {
			return false
		}
		seen[v] = struct{}{}
	}
	return true
}

// ValidateStructure enforces per-item shape. It runs as part of
// ValidateForPublish (publish and import of a published version); working-copy
// saves skip it — an incomplete draft is legal until publication.
func (v *ExerciseVersion) ValidateStructure() error {
	if len(v.Variants) == 0 {
		return ErrVersionNoVariants.Err()
	}
	canonicalTasks := v.Variants[0].Tasks
	taskCount := len(canonicalTasks)
	for variantIndex, variant := range v.Variants {
		if len(variant.Tasks) != taskCount {
			return ErrTaskCountMismatch.Err()
		}
		for taskIndex, task := range variant.Tasks {
			// Legacy persisted snapshots may predate task IDs. New saves receive
			// canonical non-nil IDs in normalizeContentIDs before arriving here;
			// for old all-nil snapshots position remains the only available key.
			if (canonicalTasks[taskIndex].ID != uuid.Nil && task.ID != canonicalTasks[taskIndex].ID) ||
				(canonicalTasks[taskIndex].ID == uuid.Nil && task.ID != uuid.Nil) {
				return ErrTaskIdentityMismatch.WithContext("task", task.Name).Err()
			}
			if variantIndex > 0 && task.Difficulty != canonicalTasks[taskIndex].Difficulty {
				return ErrTaskDifficultyMismatch.WithContext("task", task.Name).Err()
			}
			name := strings.TrimSpace(task.Name)
			if n := utf8.RuneCountInString(name); n < nameMinLen || n > nameMaxLen {
				return ErrTaskNameInvalid.WithContext("task", task.Name).Err()
			}
			if !task.Difficulty.Valid() {
				return ErrTaskDifficultyInvalid.WithContext("task", task.Name).Err()
			}
			if !validFlag(task.Flag) {
				return ErrFlagSourceInvalid.WithContext("task", task.Name).Err()
			}
			if err := validateHints(task, canonicalTasks[taskIndex]); err != nil {
				return err
			}
			keys := make(map[string]struct{}, len(task.Placeholders))
			for _, placeholder := range task.Placeholders {
				if placeholder.Key == "" {
					continue
				} // legacy draft; normalized on save
				if _, duplicate := keys[placeholder.Key]; duplicate {
					return ErrPlaceholderInvalid.WithContext("task", task.Name).Err()
				}
				keys[placeholder.Key] = struct{}{}
			}
		}
		if err := variant.Topology.validateStructure(); err != nil {
			return err
		}
		if err := variant.Topology.validateFlagEnvTargets(variant.Tasks); err != nil {
			return err
		}
	}
	return nil
}

// ValidateForPublish runs the full validation set: structure plus per-variant
// topology graph rules and placeholder resolution. It is the only business
// validation a working copy ever meets.
func (v *ExerciseVersion) ValidateForPublish() error {
	if err := v.ValidateStructure(); err != nil {
		return err
	}
	for _, variant := range v.Variants {
		if err := variant.Topology.validateGraph(variant.Tasks); err != nil {
			return err
		}
		for _, task := range variant.Tasks {
			if !hasTaskDescriptionContent(task.Description) {
				return ErrTaskDescriptionRequired.WithContext("task", task.Name).Err()
			}
			for _, hint := range task.Hints {
				if !hasHintTextContent(hint.Text) {
					return ErrHintTextRequired.WithContext("task", task.Name).Err()
				}
			}
			if !task.LinkedDeviceID.Valid && len(task.Flag) == 0 {
				return ErrFlagSourceInvalid.WithContext("task", task.Name).Err()
			}
			if !task.LinkedDeviceID.Valid && (len(task.Flag) != 1 || strings.HasPrefix(task.Flag[0], flagpattern.TemplatePrefix)) {
				return ErrFlagMultiNeedsDevice.WithContext("task", task.Name).Err()
			}
			if err := variant.Topology.validatePlaceholders(task.Placeholders); err != nil {
				return err
			}
			if err := validateInlinePlaceholderRefs(task.Description, task.Placeholders); err != nil {
				return err
			}
		}
	}
	return nil
}

// HasInfrastructure reports whether any variant has a lab topology (devices):
// such an exercise needs an event that allows infrastructure.
func HasInfrastructure(variants []Variant) bool {
	for _, variant := range variants {
		if len(variant.Topology.Devices) > 0 {
			return true
		}
	}
	return false
}

// CollectFileIDs returns every attachment FileID across a variant set —
// consumed by the media reference sync on draft save.
func CollectFileIDs(variants []Variant) []uuid.UUID {
	var ids []uuid.UUID
	for _, v := range variants {
		for _, t := range v.Tasks {
			for _, a := range t.Attachments {
				ids = append(ids, a.FileID)
			}
		}
	}
	return ids
}
