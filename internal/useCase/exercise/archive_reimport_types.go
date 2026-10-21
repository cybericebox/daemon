package exercise

import (
	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// ImportTarget is an explicit per-folder destination. Omitted preview targets
// are nonbinding suggestions; omitted commit targets retain legacy NEW behavior.
type ImportTarget struct {
	ItemKey    string    `json:"ItemKey"`
	Mode       string    `json:"Mode"` // new | update
	ExerciseID uuid.UUID `json:"ExerciseID,omitzero"`
}

type PreviewArchiveInput struct {
	UploadID        uuid.UUID      `json:"UploadID"`
	FileID          uuid.UUID      `json:"FileID"`
	Password        string         `json:"Password"`
	OwnerEventID    *uuid.UUID     `json:"OwnerEventID"`
	FixedExerciseID uuid.UUID      `json:"FixedExerciseID"`
	Targets         []ImportTarget `json:"Targets"`
}

type ImportTargetExercise struct {
	ID             uuid.UUID  `json:"ID"`
	Name           string     `json:"Name"`
	Scope          string     `json:"Scope"`
	OwnerEventID   *uuid.UUID `json:"OwnerEventID"`
	OwnerEventName string     `json:"OwnerEventName"`
	OwnerEventTag  string     `json:"OwnerEventTag"`
	HasDraft       bool       `json:"HasDraft"`
}

type ImportCountChange struct {
	Added   int `json:"Added"`
	Removed int `json:"Removed"`
	Changed int `json:"Changed"`
}
type ImportFileChange struct {
	Added   int `json:"Added"`
	Removed int `json:"Removed"`
}
type ImportImageChange struct {
	New    int `json:"New"`
	Reused int `json:"Reused"`
}
type ImportSubtaskCount struct {
	Before int `json:"Before"`
	After  int `json:"After"`
}

type ArchiveImportSummary struct {
	Variants     ImportCountChange  `json:"Variants"`
	Subtasks     ImportSubtaskCount `json:"Subtasks"`
	FlagsChanged int                `json:"FlagsChanged"`
	Files        ImportFileChange   `json:"Files"`
	Images       ImportImageChange  `json:"Images"`
	// MetadataChanged includes Name/Description/Tags and the version AdminNote.
	MetadataChanged bool `json:"MetadataChanged"`
}

type ImportSourceVersion struct {
	Position int                         `json:"Position"` // zero-based source snapshot position; -1 when the archive has no versions
	Status   exerciseModel.VersionStatus `json:"Status"`
}

type ArchiveImportPreviewItem struct {
	ItemKey          string                `json:"ItemKey"`
	SourceExerciseID *uuid.UUID            `json:"SourceExerciseID"`
	Name             string                `json:"Name"`
	SourceVersion    ImportSourceVersion   `json:"SourceVersion"`
	SuggestedTarget  ImportTarget          `json:"SuggestedTarget"`
	Target           ImportTarget          `json:"Target"`
	TargetExercise   *ImportTargetExercise `json:"TargetExercise"`
	SourceIDMismatch bool                  `json:"SourceIDMismatch"`
	// ConfirmationRevision binds immutable source/item/snapshot and selected
	// target/current content. No password, flags or secret values are returned.
	ConfirmationRevision string               `json:"ConfirmationRevision"`
	ReplacesDraft        bool                 `json:"ReplacesDraft"`
	Summary              ArchiveImportSummary `json:"Summary"`
}

type ArchiveImportPreview struct {
	Items []ArchiveImportPreviewItem `json:"Items"`
}
