package eventFormModel

import (
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// InputFile is the answer type of a question that takes one attached file.
const InputFile = "file"

// MaxAnswerFileMB is the platform cap on a file question's size limit.
const MaxAnswerFileMB = 25

// DefaultAnswerFileMB applies when a file question sets no limit.
const DefaultAnswerFileMB = 10

// AnswerScope tells whose answers a file belongs to.
type AnswerScope string

const (
	AnswerScopeParticipant AnswerScope = "participant"
	AnswerScopeTeam        AnswerScope = "team"
)

func (s AnswerScope) Valid() bool { return s == AnswerScopeParticipant || s == AnswerScopeTeam }

// FileLimitBytes is the size limit of a file question.
func FileLimitBytes(block eventContentModel.Block) int64 {
	mb := block.MaxSizeMB
	if mb <= 0 {
		mb = DefaultAnswerFileMB
	}
	return int64(min(mb, MaxAnswerFileMB)) << 20
}

// AnswerFile is an uploaded file waiting for, or attached to, one answer.
// Owner is the participant (participant scope) or the team (team scope) the
// answer belongs to once it is saved.
type AnswerFile struct {
	FileID      uuid.UUID
	EventID     uuid.UUID
	Scope       AnswerScope
	FieldKey    string
	Name        string
	SizeBytes   int64
	ContentType string
	UploadedBy  uuid.UUID
	OwnerID     uuid.NullUUID
	AttachedAt  *time.Time
	CreatedAt   time.Time
}

// NewAnswerFile records an upload for a file question.
func NewAnswerFile(fileID, eventID uuid.UUID, scope AnswerScope, fieldKey, name string, size int64, contentType string, uploadedBy uuid.UUID, now time.Time) AnswerFile {
	return AnswerFile{FileID: fileID, EventID: eventID, Scope: scope, FieldKey: fieldKey, Name: name, SizeBytes: size, ContentType: contentType, UploadedBy: uploadedBy, CreatedAt: now}
}

// CanAnswer reports whether the file may be the answer to a question of this
// event and scope. A pending upload is claimed by its uploader (uuid.Nil is
// event staff, who may use any pending upload of the event); an attached one
// stays with its owner.
func (f AnswerFile) CanAnswer(eventID uuid.UUID, scope AnswerScope, fieldKey string, actor, owner uuid.UUID) bool {
	if f.EventID != eventID || f.Scope != scope || f.FieldKey != fieldKey {
		return false
	}
	if f.AttachedAt != nil {
		return f.OwnerID.Valid && f.OwnerID.UUID == owner
	}
	return actor == uuid.Nil || f.UploadedBy == actor
}

// VisibleTo reports whether a participant may download the file: their own
// pending upload, their own answer, or an answer of their team.
func (f AnswerFile) VisibleTo(userID uuid.UUID, teamID *uuid.UUID) bool {
	if f.AttachedAt == nil {
		return f.UploadedBy == userID
	}
	if !f.OwnerID.Valid {
		return false
	}
	switch f.Scope {
	case AnswerScopeParticipant:
		return f.OwnerID.UUID == userID
	case AnswerScopeTeam:
		return teamID != nil && f.OwnerID.UUID == *teamID
	default:
		return false
	}
}

// Value is the answer stored for the question: the file reference with the
// metadata the organizer tables show.
func (f AnswerFile) Value() map[string]any {
	return map[string]any{"id": f.FileID.String(), "name": f.Name, "size": f.SizeBytes, "contentType": f.ContentType}
}

// AnswerFileID reads the file reference of a file answer.
func AnswerFileID(value any) (uuid.UUID, bool) {
	object, ok := value.(map[string]any)
	if !ok {
		return uuid.Nil, false
	}
	text, ok := object["id"].(string)
	if !ok {
		return uuid.Nil, false
	}
	id, err := uuid.FromString(text)
	return id, err == nil && id != uuid.Nil
}

func validateFileQuestion(block eventContentModel.Block) error {
	if len(block.FileTypes) == 0 {
		return fmt.Errorf("field %q must allow at least one file type", block.Key)
	}
	seen := make(map[string]struct{}, len(block.FileTypes))
	for _, kind := range block.FileTypes {
		if !validFileKind(kind) {
			return fmt.Errorf("field %q allows an unsupported file type %q", block.Key, kind)
		}
		if _, exists := seen[kind]; exists {
			return fmt.Errorf("field %q repeats file type %q", block.Key, kind)
		}
		seen[kind] = struct{}{}
	}
	if block.MaxSizeMB < 0 || block.MaxSizeMB > MaxAnswerFileMB {
		return fmt.Errorf("field %q size limit must be between 1 and %d MB", block.Key, MaxAnswerFileMB)
	}
	return nil
}
