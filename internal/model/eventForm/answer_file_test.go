package eventFormModel

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func TestFileQuestionLimitAndTypes(t *testing.T) {
	block := eventContentModel.Block{Key: "cv", Input: InputFile, FileTypes: []string{"pdf"}}
	if FileLimitBytes(block) != 10<<20 {
		t.Fatal("default limit must be 10 MB")
	}
	block.MaxSizeMB = 100
	if FileLimitBytes(block) != MaxAnswerFileMB<<20 {
		t.Fatal("limit must be capped by the platform maximum")
	}
	if !AllowsFile(block, FileKindPDF) || AllowsFile(block, FileKindArchive) {
		t.Fatal("only the allowed kinds may answer")
	}
	for _, bad := range []eventContentModel.Block{
		{Key: "cv", Input: InputFile},
		{Key: "cv", Input: InputFile, FileTypes: []string{"exe"}},
		{Key: "cv", Input: InputFile, FileTypes: []string{"pdf", "pdf"}},
		{Key: "cv", Input: InputFile, FileTypes: []string{"pdf"}, MaxSizeMB: MaxAnswerFileMB + 1},
	} {
		form := Form{Document: eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "cv", Type: eventContentModel.BlockField, Key: "cv", Label: "CV", Input: InputFile, FileTypes: bad.FileTypes, MaxSizeMB: bad.MaxSizeMB}}}}
		if err := form.Validate(); err == nil {
			t.Fatalf("file question %+v must be rejected", bad)
		}
	}
}

func TestFileQuestionIsNotAConditionSource(t *testing.T) {
	form := conditionalForm(eventContentModel.Block{ID: "cv", Type: eventContentModel.BlockField, Key: "cv", Input: InputFile, Label: "CV", FileTypes: []string{"pdf"}}, eventContentModel.Condition{FieldKey: "cv", Operator: "equals", Value: "x"})
	if err := form.Validate(); err == nil {
		t.Fatal("a file question cannot drive a condition")
	}
}

func TestFileAnswerMustReferenceUpload(t *testing.T) {
	form := Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "cv", Type: eventContentModel.BlockField, Key: "cv", Input: InputFile, Label: "CV", Required: true, FileTypes: []string{"pdf"}}}}}
	if err := form.ValidateAnswers(map[string]any{"cv": "cv.pdf"}); err == nil {
		t.Fatal("a file answer must be a file reference")
	}
	if err := form.ValidateAnswers(map[string]any{"cv": map[string]any{"id": uuid.Must(uuid.NewV7()).String()}}); err != nil {
		t.Fatalf("file reference must pass: %v", err)
	}
	if err := form.ValidateAnswers(map[string]any{"cv": ""}); err == nil {
		t.Fatal("a cleared required file must be reported missing")
	}
	form.Document.Blocks[0].Required = false
	if err := form.ValidateAnswers(map[string]any{"cv": ""}); err != nil {
		t.Fatalf("a cleared optional file is allowed: %v", err)
	}
}

func TestAnswerFileOwnership(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	eventID, uploader, other, team := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	file := NewAnswerFile(uuid.Must(uuid.NewV7()), eventID, AnswerScopeTeam, "cv", "cv.pdf", 10, "application/pdf", uploader, now)
	if !file.CanAnswer(eventID, AnswerScopeTeam, "cv", uploader, team) || file.CanAnswer(eventID, AnswerScopeTeam, "cv", other, team) {
		t.Fatal("a pending upload belongs to its uploader")
	}
	if !file.CanAnswer(eventID, AnswerScopeTeam, "cv", uuid.Nil, team) {
		t.Fatal("staff may use a pending upload of the event")
	}
	if file.CanAnswer(eventID, AnswerScopeParticipant, "cv", uploader, uploader) || file.CanAnswer(eventID, AnswerScopeTeam, "other", uploader, team) {
		t.Fatal("scope and question must match")
	}
	if !file.VisibleTo(uploader, nil) || file.VisibleTo(other, &team) {
		t.Fatal("only the uploader sees a pending upload")
	}
	file.OwnerID, file.AttachedAt = uuid.NullUUID{UUID: team, Valid: true}, &now
	if !file.CanAnswer(eventID, AnswerScopeTeam, "cv", other, team) || file.CanAnswer(eventID, AnswerScopeTeam, "cv", uploader, other) {
		t.Fatal("an attached file stays with its owner")
	}
	if !file.VisibleTo(other, &team) || file.VisibleTo(uploader, nil) {
		t.Fatal("a team answer is visible to the team")
	}
}
