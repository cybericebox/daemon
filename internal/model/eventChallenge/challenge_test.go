package eventChallengeModel_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestNew_CopiesPublicTaskWithoutFlag(t *testing.T) {
	challenge, err := eventChallengeModel.New(uuid.Must(uuid.NewV7()), exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Difficulty: exerciseModel.DifficultyEasy, Flag: []string{"ICE{secret}"}}, 0, time.Now())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if strings.Contains(string(challenge.Snapshot), "ICE{secret}") || challenge.Points != 100 {
		t.Fatalf("unsafe or invalid snapshot: %+v", challenge)
	}
}

func TestHintCost_EventPriceElseFree(t *testing.T) {
	priced, free := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var hints []eventChallengeModel.Hint
	// Boards saved before levels carried the catalog cost; it no longer counts.
	if err := json.Unmarshal([]byte(`[{"id":"`+priced.String()+`","cost":40,"text":"a"},{"id":"`+free.String()+`","cost":25,"level":"steps","text":"b"}]`), &hints); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if hints[0].Level != exerciseModel.HintLevelNudge || hints[1].Level != exerciseModel.HintLevelSteps {
		t.Fatalf("levels=%+v", hints)
	}
	challenge := eventChallengeModel.EventChallenge{Hints: hints, HintCosts: map[uuid.UUID]int32{priced: 15}}
	if cost, found := challenge.HintCost(priced); !found || cost != 15 {
		t.Fatalf("priced cost=%d found=%t", cost, found)
	}
	if cost, found := challenge.HintCost(free); !found || cost != 0 {
		t.Fatalf("unpriced cost=%d found=%t", cost, found)
	}
	if _, found := challenge.HintCost(uuid.Must(uuid.NewV7())); found {
		t.Fatal("unknown hint found")
	}
}

func TestSnapshotForTask_CarriesPlaceholders(t *testing.T) {
	task := exerciseModel.Task{Name: "t", Difficulty: exerciseModel.DifficultyEasy, Placeholders: []exerciseModel.Placeholder{
		{Key: "ph_a", Kind: exerciseModel.PlaceholderIP, IPReference: "vpn", LastOctet: 5, AsLink: true, Scheme: "https", Port: 8443, Path: "/x"},
	}}
	snapshot, err := eventChallengeModel.SnapshotForTask(task)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Placeholders []map[string]any `json:"placeholders"`
	}
	if err := json.Unmarshal(snapshot, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Placeholders) != 1 || got.Placeholders[0]["as_link"] != true || got.Placeholders[0]["scheme"] != "https" || got.Placeholders[0]["port"] != float64(8443) {
		t.Fatalf("placeholders lost: %s", snapshot)
	}
	plain, _ := eventChallengeModel.SnapshotForTask(exerciseModel.Task{Name: "t", Difficulty: exerciseModel.DifficultyEasy})
	if strings.Contains(string(plain), "placeholders") {
		t.Fatalf("empty placeholders must be omitted: %s", plain)
	}
}

func TestSetMaxFlagAttempts(t *testing.T) {
	challenge, err := eventChallengeModel.New(uuid.Must(uuid.NewV7()), exerciseModel.Task{ID: uuid.Must(uuid.NewV7()), Name: "Task", Difficulty: exerciseModel.DifficultyEasy}, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if challenge.MaxFlagAttempts != nil {
		t.Fatal("a new task has no override")
	}
	three := int32(3)
	if err = challenge.SetMaxFlagAttempts(&three); err != nil || challenge.MaxFlagAttempts == nil || *challenge.MaxFlagAttempts != 3 {
		t.Fatalf("set: %v %v", challenge.MaxFlagAttempts, err)
	}
	three = 9
	if *challenge.MaxFlagAttempts != 3 {
		t.Fatal("the task must not alias the caller's value")
	}
	zero := int32(0)
	if err = challenge.SetMaxFlagAttempts(&zero); err == nil || *challenge.MaxFlagAttempts != 3 {
		t.Fatalf("zero must be refused and keep the value: %v", err)
	}
	if err = challenge.SetMaxFlagAttempts(nil); err != nil || challenge.MaxFlagAttempts != nil {
		t.Fatalf("clear: %v %v", challenge.MaxFlagAttempts, err)
	}
}
