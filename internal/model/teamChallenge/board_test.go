package teamChallengeModel_test

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"

	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

func TestLockedSnapshotKeepsOnlyNameAndDifficulty(t *testing.T) {
	fileID := uuid.Must(uuid.NewV7())
	full := json.RawMessage(`{"name":"Web 1","description":{"blocks":[]},"difficulty":2,"attachments":[{"file_id":"` + fileID.String() + `","name":"a.zip"}]}`)
	got, err := teamChallengeModel.LockedSnapshot(full)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(got, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 2 || string(fields["name"]) != `"Web 1"` || string(fields["difficulty"]) != `2` {
		t.Fatalf("locked snapshot = %s", got)
	}
}

func TestLockedSnapshotRejectsCorruptSnapshot(t *testing.T) {
	if _, err := teamChallengeModel.LockedSnapshot(json.RawMessage(`[`)); err == nil {
		t.Fatal("a corrupt snapshot must not be passed through when locked")
	}
}

func TestSnapshotAttachments(t *testing.T) {
	fileID := uuid.Must(uuid.NewV7())
	got, err := teamChallengeModel.SnapshotAttachments(json.RawMessage(`{"name":"x","attachments":[{"file_id":"` + fileID.String() + `","name":"a.zip"}]}`))
	if err != nil || len(got) != 1 || got[0].FileID != fileID || got[0].Name != "a.zip" {
		t.Fatalf("attachments = %+v, %v", got, err)
	}
	none, err := teamChallengeModel.SnapshotAttachments(json.RawMessage(`{"name":"x"}`))
	if err != nil || len(none) != 0 {
		t.Fatalf("no attachments = %+v, %v", none, err)
	}
}

func TestPrerequisitesLock(t *testing.T) {
	if teamChallengeModel.PrerequisitesLock(nil) {
		t.Fatal("no prerequisites never locks")
	}
	if teamChallengeModel.PrerequisitesLock([]bool{true, true}) {
		t.Fatal("solved prerequisites unlock")
	}
	if !teamChallengeModel.PrerequisitesLock([]bool{true, false}) {
		t.Fatal("an unsolved prerequisite locks")
	}
}
