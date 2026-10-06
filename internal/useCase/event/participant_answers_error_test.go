package event

import (
	"errors"
	"net/http"
	"testing"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	pkgerr "github.com/cybericebox/daemon/pkg/err"
)

func TestMissingRequiredAnswerIsABadRequestNamingTheField(t *testing.T) {
	form := eventFormModel.Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "school", Type: eventContentModel.BlockField, Label: "School", Key: "school", Input: "text", Required: true},
	}}}
	err := participantAnswersError(form.ValidateAnswers(map[string]any{}))
	var appErr pkgerr.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("want an application error, got %v", err)
	}
	status := appErr.StatusCode()
	if status.HTTPCode() != http.StatusBadRequest {
		t.Fatalf("http code = %d, want 400", status.HTTPCode())
	}
	if status.Details()[participantModel.DetailField] != "school" {
		t.Fatalf("details = %v, want the field key (%v)", status.Details(), err)
	}
	if !errors.Is(err, participantModel.ErrParticipantFieldRequired.Err()) {
		t.Fatalf("want ErrParticipantFieldRequired, got %v", err)
	}
}
