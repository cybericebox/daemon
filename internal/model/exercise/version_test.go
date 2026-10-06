package exerciseModel_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func validTask() exerciseModel.Task {
	return exerciseModel.Task{
		ID:          uuid.Must(uuid.NewV7()),
		Name:        "Find the flag",
		Description: json.RawMessage(`{"root":{"children":[{"type":"paragraph","children":[{"type":"text","text":"Find the flag"}]}]}}`),
		Difficulty:  exerciseModel.DifficultyEasy,
		Flag:        []string{"ICE{x}"},
	}
}

func validVariant() exerciseModel.Variant {
	return exerciseModel.Variant{Tasks: []exerciseModel.Task{validTask()}}
}

func TestValidateStructure(t *testing.T) {
	twoTasks := validVariant()
	twoTasks.Tasks = append(twoTasks.Tasks, validTask())

	badFlagList := validVariant()
	badFlagList.Tasks[0].Flag = []string{" "}

	emptyFlag := validVariant()
	emptyFlag.Tasks[0].Flag = nil

	multiFlag := validVariant()
	multiFlag.Tasks[0].Flag = []string{"ICE{a}", "ICE{b}"}

	badDifficulty := validVariant()
	badDifficulty.Tasks[0].Difficulty = "nightmare"

	badTaskName := validVariant()
	badTaskName.Tasks[0].Name = "ab"

	badDeviceName := validVariant()
	badDeviceName.Topology.Devices = []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "Bad_Name", Type: exerciseModel.DeviceTypeContainer}}

	badEndpointArity := validVariant()
	badEndpointArity.Topology.Connections = []exerciseModel.Connection{{Endpoints: []exerciseModel.Endpoint{{Kind: exerciseModel.EndpointVPN}}}}

	cases := []struct {
		name     string
		variants []exerciseModel.Variant
		wantErr  error
	}{
		{"no variants", nil, exerciseModel.ErrVersionNoVariants.Err()},
		{"task count mismatch", []exerciseModel.Variant{validVariant(), twoTasks}, exerciseModel.ErrTaskCountMismatch.Err()},
		{"flag entry blank", []exerciseModel.Variant{badFlagList}, exerciseModel.ErrFlagSourceInvalid.Err()},
		{"flag empty is valid (random at deploy)", []exerciseModel.Variant{emptyFlag}, nil},
		{"flag with multiple candidates is valid", []exerciseModel.Variant{multiFlag}, nil},
		{"bad difficulty", []exerciseModel.Variant{badDifficulty}, exerciseModel.ErrTaskDifficultyInvalid.Err()},
		{"bad task name", []exerciseModel.Variant{badTaskName}, exerciseModel.ErrTaskNameInvalid.Err()},
		{"bad device name", []exerciseModel.Variant{badDeviceName}, exerciseModel.ErrDeviceNameInvalid.Err()},
		{"bad endpoint arity", []exerciseModel.Variant{badEndpointArity}, exerciseModel.ErrConnectionArityInvalid.Err()},
		{"ok", []exerciseModel.Variant{validVariant()}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := exerciseModel.ExerciseVersion{Variants: tc.variants}
			err := v.ValidateStructure()
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateStructure_RequiresICEFlagFormat(t *testing.T) {
	cases := []struct {
		name  string
		value string
		valid bool
	}{
		{"simple", "ICE{one}", true},
		{"punctuation", "ICE{a_b-123!}", true},
		{"wrong prefix", "FLAG{one}", false},
		{"wrong case", "ice{one}", false},
		{"empty body", "ICE{}", false},
		{"space inside", "ICE{one two}", false},
		{"tab inside", "ICE{one\ttwo}", false},
		{"unicode space inside", "ICE{one\u00a0two}", false},
		{"nested braces", "ICE{one{two}}", false},
		{"space outside", " ICE{one}", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			variant := validVariant()
			variant.Tasks[0].Flag = []string{tc.value}
			err := (&exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}).ValidateStructure()
			if tc.valid && err != nil {
				t.Fatalf("valid flag %q rejected: %v", tc.value, err)
			}
			if !tc.valid && !errors.Is(err, exerciseModel.ErrFlagSourceInvalid.Err()) {
				t.Fatalf("invalid flag %q: want ErrFlagSourceInvalid, got %v", tc.value, err)
			}
		})
	}
}

func TestFlagCandidates_DraftAndPublicationRules(t *testing.T) {
	for _, tc := range []struct {
		name           string
		flags          []string
		structureValid bool
		publishValid   bool
	}{
		{"empty static draft", nil, true, false},
		{"one static fixed", []string{"ICE{one}"}, true, true},
		{"two static fixed", []string{"ICE{one}", "ICE{two}"}, true, false},
		{"static template", []string{`template:ICE{\d}`}, true, false},
		{"duplicate fixed", []string{"ICE{one}", "ICE{one}"}, false, false},
		{"duplicate template", []string{`template:ICE{\d}`, `template:ICE{\d}`}, false, false},
		{"different kinds", []string{"ICE{1}", `template:ICE{[1]}`}, true, false},
		{"bad template", []string{`template:ICE{[Z-A]}`}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variant := validVariant()
			variant.Tasks[0].Flag = tc.flags
			version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}
			if got := version.ValidateStructure() == nil; got != tc.structureValid {
				t.Fatalf("structure valid=%t, want %t", got, tc.structureValid)
			}
			if got := version.ValidateForPublish() == nil; got != tc.publishValid {
				t.Fatalf("publish valid=%t, want %t", got, tc.publishValid)
			}
		})
	}
}

func TestValidateForPublish_RejectsDeletedInlinePlaceholderReference(t *testing.T) {
	task := validTask()
	task.Description = json.RawMessage(`{"root":{"children":[{"type":"paragraph","children":[{"type":"text","text":"before "},{"type":"variable","varName":"ph_missing"},{"type":"text","text":" after"}]}]}}`)
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}}}
	if err := version.ValidateStructure(); err != nil {
		t.Fatalf("draft can retain an unresolved token while editing: %v", err)
	}
	if !errors.Is(version.ValidateForPublish(), exerciseModel.ErrPlaceholderInvalid.Err()) {
		t.Fatal("publication must reject an inline token with no definition")
	}
	task.Placeholders = []exerciseModel.Placeholder{{Key: "ph_missing", Kind: exerciseModel.PlaceholderIP, IPReference: "static", Octets1to3: "10.0.0", LastOctet: 5}}
	version.Variants[0].Tasks[0] = task
	if err := version.ValidateForPublish(); err != nil {
		t.Fatalf("defined inline placeholder rejected: %v", err)
	}
}

func TestValidateForPublish_RequiresMeaningfulTaskDescription(t *testing.T) {
	for _, description := range []json.RawMessage{
		nil,
		json.RawMessage(`{"blocks":[]}`),
		json.RawMessage(`{"root":{"children":[{"type":"paragraph","children":[]}]}}`),
		json.RawMessage(`{"root":{"children":[{"type":"paragraph","children":[{"type":"text","text":"   "}]}]}}`),
	} {
		task := validTask()
		task.Description = description
		version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}}}
		if err := version.ValidateStructure(); err != nil {
			t.Fatalf("incomplete description must be allowed in a draft: %v", err)
		}
		if !errors.Is(version.ValidateForPublish(), exerciseModel.ErrTaskDescriptionRequired.Err()) {
			t.Fatalf("publication must reject an empty description: %s", description)
		}
	}
	for _, description := range []json.RawMessage{
		json.RawMessage(`{"root":{"children":[{"type":"paragraph","children":[{"type":"text","text":"Read this"}]}]}}`),
		json.RawMessage(`{"root":{"children":[{"type":"paragraph","children":[{"type":"variable","varName":"ph_ip"}]}]}}`),
	} {
		task := validTask()
		task.Description = description
		task.Placeholders = []exerciseModel.Placeholder{{Key: "ph_ip", Kind: exerciseModel.PlaceholderIP, IPReference: "static", Octets1to3: "10.0.0", LastOctet: 5}}
		version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}}}
		if err := version.ValidateForPublish(); err != nil {
			t.Fatalf("meaningful description rejected: %v", err)
		}
	}
}

func TestValidateStructure_RequiresStableTaskIdentityAndDifficulty(t *testing.T) {
	first := validVariant()
	second := validVariant()
	second.Tasks[0].ID = first.Tasks[0].ID

	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{first, second}}
	if err := version.ValidateStructure(); err != nil {
		t.Fatalf("matching variants rejected: %v", err)
	}

	second.Tasks[0].ID = uuid.Must(uuid.NewV7())
	if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrTaskIdentityMismatch.Err()) {
		t.Fatalf("want task identity mismatch, got %v", err)
	}

	second.Tasks[0].ID = first.Tasks[0].ID
	second.Tasks[0].Difficulty = exerciseModel.DifficultyHard
	if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrTaskDifficultyMismatch.Err()) {
		t.Fatalf("want task difficulty mismatch, got %v", err)
	}
}

func TestVersionStatusPredicates(t *testing.T) {
	if !exerciseModel.VersionStatusDraft.IsDraft() ||
		!exerciseModel.VersionStatusPublished.IsPublished() ||
		!exerciseModel.VersionStatusUnpublished.IsUnpublished() {
		t.Fatal("status predicates broken")
	}
	if exerciseModel.VersionStatus("bogus").Valid() {
		t.Fatal("bogus status must be invalid")
	}
}

func TestCollectFileIDs(t *testing.T) {
	f1, f2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	v := validVariant()
	v.Tasks[0].Attachments = []exerciseModel.AttachmentRef{{FileID: f1, Name: "a.zip"}, {FileID: f2, Name: "b.pcap"}}
	ids := exerciseModel.CollectFileIDs([]exerciseModel.Variant{v})
	if len(ids) != 2 || ids[0] != f1 || ids[1] != f2 {
		t.Fatalf("got %v", ids)
	}
}

func TestHint_DecodesLegacyCostAsNudge(t *testing.T) {
	var hint exerciseModel.Hint
	if err := json.Unmarshal([]byte(`{"id":"00000000-0000-0000-0000-000000000001","text":"Look","cost":30}`), &hint); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if hint.Level != exerciseModel.HintLevelNudge || hint.Text != "Look" {
		t.Fatalf("legacy hint=%+v", hint)
	}
	if err := json.Unmarshal([]byte(`{"id":"00000000-0000-0000-0000-000000000001","text":"Look","level":"steps"}`), &hint); err != nil || hint.Level != exerciseModel.HintLevelSteps {
		t.Fatalf("hint=%+v err=%v", hint, err)
	}
}

func TestValidateStructure_HintLevels(t *testing.T) {
	hintID := uuid.Must(uuid.NewV7())
	first := validVariant()
	second := validVariant()
	second.Tasks[0].ID = first.Tasks[0].ID
	first.Tasks[0].Hints = []exerciseModel.Hint{{ID: hintID, Text: "a", Level: exerciseModel.HintLevelDirection}}
	second.Tasks[0].Hints = []exerciseModel.Hint{{ID: hintID, Text: "b", Level: exerciseModel.HintLevelDirection}}
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{first, second}}
	if err := version.ValidateStructure(); err != nil {
		t.Fatalf("matching hint levels rejected: %v", err)
	}

	second.Tasks[0].Hints[0].Level = exerciseModel.HintLevelNearSolution
	if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrHintsInvalid.Err()) {
		t.Fatalf("want hint level mismatch, got %v", err)
	}

	first.Tasks[0].Hints[0].Level = "cheap"
	second.Tasks[0].Hints[0].Level = "cheap"
	if err := version.ValidateStructure(); !errors.Is(err, exerciseModel.ErrHintsInvalid.Err()) {
		t.Fatalf("want unknown hint level rejected, got %v", err)
	}
}

func TestValidateForPublish_RequiresHintText(t *testing.T) {
	hintID := uuid.Must(uuid.NewV7())
	withHint := func(text string) exerciseModel.ExerciseVersion {
		task := validTask()
		task.Hints = []exerciseModel.Hint{{ID: hintID, Text: text, Level: exerciseModel.HintLevelNudge}}
		return exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{task}}}}
	}
	for _, text := range []string{
		"",
		"   ",
		`{"root":{"type":"root","children":[{"type":"paragraph","children":[]}]}}`,
		`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"  "}]},{"type":"paragraph","children":[]}]}}`,
	} {
		version := withHint(text)
		if err := version.ValidateStructure(); err != nil {
			t.Fatalf("a draft may keep an empty hint: %v", err)
		}
		if err := version.ValidateForPublish(); !errors.Is(err, exerciseModel.ErrHintTextRequired.Err()) {
			t.Fatalf("hint %q: want ErrHintTextRequired, got %v", text, err)
		}
	}
	for _, text := range []string{
		"Look at the headers",
		`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Look"}]}]}}`,
	} {
		version := withHint(text)
		if err := version.ValidateForPublish(); err != nil {
			t.Fatalf("hint %q rejected: %v", text, err)
		}
	}
}

// A variant counts as infrastructure only with a real device: no topology, an
// empty one, or only the vpn/internet singletons are not a lab.
func TestHasInfrastructure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		variants []exerciseModel.Variant
		want     bool
	}{
		{"no variants", nil, false},
		{"no topology", []exerciseModel.Variant{{}}, false},
		{"empty devices", []exerciseModel.Variant{{Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{}}}}, false},
		{"networks without devices", []exerciseModel.Variant{{Topology: exerciseModel.Topology{VPN: exerciseModel.NetworkSpec{Enabled: true}, Internet: exerciseModel.NetworkSpec{Enabled: true}}}}, false},
		{"device in a later variant", []exerciseModel.Variant{{}, {Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "host-1"}}}}}, true},
	} {
		if got := exerciseModel.HasInfrastructure(tc.variants); got != tc.want {
			t.Errorf("%s: HasInfrastructure = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestValidateForPublish_MultiFlagNeedsDeviceHasOwnError(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		want  error
	}{
		{"several flags", []string{"ICE{one}", "ICE{two}"}, exerciseModel.ErrFlagMultiNeedsDevice.Err()},
		{"template flag", []string{`template:ICE{\d}`}, exerciseModel.ErrFlagMultiNeedsDevice.Err()},
		{"no flag", nil, exerciseModel.ErrFlagSourceInvalid.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			variant := validVariant()
			variant.Tasks[0].Flag = tc.flags
			err := (&exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}).ValidateForPublish()
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, err)
			}
		})
	}
}
