package eventself

import (
	"encoding/json"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"testing"
)

func TestLabDTOFrozenWireNullabilityAndDecimalRevision(t *testing.T) {
	lab := event.ParticipantLabView{ID: uuid.Must(uuid.NewV7()), EventExerciseID: uuid.Must(uuid.NewV7()), Revision: "9007199254740993", RuntimeState: "preparing", SnapshotPolicy: "required"}
	got := toOwnChallengeResponse(event.OwnChallengeView{EventExerciseID: lab.EventExerciseID, Lab: &lab})
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	var inner map[string]json.RawMessage
	if err = json.Unmarshal(wire["Lab"], &inner); err != nil {
		t.Fatal(err)
	}
	if len(inner) != 11 || string(inner["Revision"]) != `"9007199254740993"` || string(inner["ClosedAt"]) != "null" || string(inner["CloseReason"]) != "null" || string(inner["RetentionUntil"]) != "null" || string(inner["CanRestart"]) != "false" {
		t.Fatalf("wire %s", raw)
	}
	raw, _ = json.Marshal(toOwnChallengeResponse(event.OwnChallengeView{EventExerciseID: lab.EventExerciseID}))
	if err = json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["Lab"]) != "null" || len(wire["EventExerciseID"]) == 0 {
		t.Fatalf("static wire %s", raw)
	}
}
