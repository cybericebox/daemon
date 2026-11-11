package exerciseModel

import "github.com/gofrs/uuid"

// What an import into an existing exercise did, or would do, to the stored working copy. The same report is the
// answer of the dry run (preview) and of the apply call. It never carries flags, answers or secret values.

// ImportMatch says how an incoming item was paired with a stored one.
type ImportMatch string

const (
	ImportMatchID    ImportMatch = "id"
	ImportMatchKey   ImportMatch = "key"
	ImportMatchName  ImportMatch = "name"
	ImportMatchOrder ImportMatch = "order"
)

// ImportItem is one task or device of a variant.
type ImportItem struct {
	// Name is the name in the document (kept, added) or in the stored copy (removed).
	Name string `json:"Name"`
	// PreviousName is the stored name of a kept task whose name changed.
	PreviousName string `json:"PreviousName,omitempty"`
	// Key is the task key (tasks only): the stored one for a kept task, the document's otherwise.
	Key string `json:"Key,omitempty"`
	// ID is the stored id a kept or removed item has. An added item gets its id when the draft is written.
	ID uuid.UUID `json:"ID,omitzero"`
	// MatchedBy is set on kept items: the stored item keeps its id (and a task its key).
	MatchedBy ImportMatch `json:"MatchedBy,omitempty"`
	// KeyChanged: a task matched by id keeps the stored key; this is true only when it could not (another task of
	// the document uses that key), so the document's key stays.
	KeyChanged bool `json:"KeyChanged,omitempty"`
}

// ImportChanges lists what stays, what is new and what leaves. The slices are never null.
type ImportChanges struct {
	Kept    []ImportItem `json:"Kept"`
	Added   []ImportItem `json:"Added"`
	Removed []ImportItem `json:"Removed"`
}

const (
	ImportVariantKept    = "kept"
	ImportVariantAdded   = "added"
	ImportVariantRemoved = "removed"
)

// ImportVariantDiff is one variant: kept (paired with a stored variant), added or removed.
type ImportVariantDiff struct {
	Status string `json:"Status"`
	// Position is the 1-based position in the document (0 for a removed variant); StoredPosition the position in
	// the stored copy (0 for an added variant).
	Position       int         `json:"Position"`
	StoredPosition int         `json:"StoredPosition"`
	MatchedBy      ImportMatch `json:"MatchedBy,omitempty"`
	// Tasks of an added variant that match a stored task (by id or key) are listed as kept: they take its identity.
	Tasks   ImportChanges `json:"Tasks"`
	Devices ImportChanges `json:"Devices"`
}

// ImportDiff is the identity pairing of the document with the stored working copy.
type ImportDiff struct {
	Variants []ImportVariantDiff `json:"Variants"`
}

// ImportIssue is one problem of the result: its public error code and message, the path in the document and the
// public context of the error (the task, device or field it names).
type ImportIssue struct {
	Code    int            `json:"Code"`
	Message string         `json:"Message"`
	Path    string         `json:"Path,omitempty"`
	Context map[string]any `json:"Context,omitempty"`
}

// Warning codes of an import report.
const (
	// ImportWarnNoExerciseID: the document has no exercise id, so it is imported as a new exercise.
	ImportWarnNoExerciseID = "no_exercise_id"
	// ImportWarnKeyConflict: a task could not keep its stored key (see ImportItem.KeyChanged).
	ImportWarnKeyConflict = "task_key_conflict"
	// ImportWarnUsedByEvents: the exercise is attached to events; see ImportUsage.
	ImportWarnUsedByEvents = "used_by_events"
	// ImportWarnRemovedHasAttempts: a task that leaves has recorded answers in an event.
	ImportWarnRemovedHasAttempts = "removed_task_has_attempts"
	// ImportWarnRunningLabs: laboratories of the exercise are running.
	ImportWarnRunningLabs = "running_labs"
)

// ImportWarning is something to know before applying. A warning never forbids.
type ImportWarning struct {
	Code    string         `json:"Code"`
	Context map[string]any `json:"Context,omitempty"`
}

// ImportUsageTask is a task that appears or disappears for an event once the draft is published and the event
// moves to it.
type ImportUsageTask struct {
	Name string `json:"Name"`
	// Attempts and Solves are the recorded answers of the event's challenge (removed tasks only).
	Attempts int `json:"Attempts,omitempty"`
	Solves   int `json:"Solves,omitempty"`
}

// ImportUsageEvent is one active attachment of the exercise in an event.
type ImportUsageEvent struct {
	EventID  uuid.UUID `json:"EventID"`
	Name     string    `json:"Name"`
	Archived bool      `json:"Archived"`
	// VersionPublished: the event runs the version published now; otherwise it is pinned to an older one.
	VersionPublished bool `json:"VersionPublished"`
	// RunningLabs are the event's team laboratories of this exercise that are running now.
	RunningLabs int `json:"RunningLabs"`
	// Removed and Added are the tasks of the event's challenge variant that would go and come, by the rule the
	// event itself applies when it moves to a new version (by task id, else by name).
	Removed []ImportUsageTask `json:"Removed"`
	Added   []ImportUsageTask `json:"Added"`
	// VariantMissing: the event's fixed variant would not exist in the result.
	VariantMissing bool `json:"VariantMissing,omitempty"`
	// Blocked: the event could not move to the result as it is: a removed task has recorded answers.
	Blocked bool `json:"Blocked"`
}

// ImportUsage tells who uses the exercise now. Nothing here changes until the draft is published, and an event
// moves to a published version only when its organizer updates the attachment.
type ImportUsage struct {
	Events []ImportUsageEvent `json:"Events"`
	// TestDeploys are the test labs of the exercise that are leased now (any author).
	TestDeploys int `json:"TestDeploys"`
}

// ImportReport is what an import item did or would do.
type ImportReport struct {
	Diff ImportDiff `json:"Diff"`
	// Issues are the errors of the result (draft validation and the publish checks). Errors of the document itself
	// refuse the request instead.
	Issues []ImportIssue `json:"Issues"`
	// Publishable: the result passes every publish check.
	Publishable bool            `json:"Publishable"`
	Warnings    []ImportWarning `json:"Warnings"`
	Usage       ImportUsage     `json:"Usage"`
}
