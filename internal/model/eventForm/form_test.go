package eventFormModel

import (
	"time"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"testing"
)

func TestFormRequiredValidationRespectsVisibility(t *testing.T) {
	form := Form{Enabled: true, Required: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "student", Type: eventContentModel.BlockField, Key: "student", Input: "checkbox", Label: "Student"},
		{ID: "university", Type: eventContentModel.BlockField, Key: "university", Input: "text", Label: "University", Required: true, Condition: &eventContentModel.Condition{FieldKey: "student", Operator: "equals", Value: true}},
	}}}
	if err := form.ValidateAnswers(map[string]any{"student": false}); err != nil {
		t.Fatalf("hidden required field must not block: %v", err)
	}
	if err := form.ValidateAnswers(map[string]any{"student": true}); err == nil {
		t.Fatal("visible required field must block")
	}
}

func TestFormAnswersRejectInvalidOption(t *testing.T) {
	form := Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "country", Type: eventContentModel.BlockField, Key: "country", Input: "select", Label: "Country", Options: []string{"UA"}}}}}
	if err := form.ValidateAnswers(map[string]any{"country": "US"}); err == nil {
		t.Fatal("invalid option must be rejected")
	}
}

func TestFormOnlyBlocksWhenEnabledAndRequired(t *testing.T) {
	if (Form{Enabled: false, Required: true}).BlocksParticipation(false) {
		t.Fatal("disabled form must not block")
	}
	if (Form{Enabled: true, Required: false}).BlocksParticipation(false) {
		t.Fatal("optional form must not block")
	}
	if !(Form{Enabled: true, Required: true}).BlocksParticipation(false) {
		t.Fatal("required enabled form must block before submission")
	}
}

func TestOptionalFormDoesNotBlockButStillValidatesItsRequiredQuestions(t *testing.T) {
	form := Form{Enabled: true, Required: false, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "name", Type: eventContentModel.BlockField, Key: "name", Input: "text", Label: "Name", Required: true}}}}
	if form.BlocksParticipation(false) {
		t.Fatal("optional form must not block before submission")
	}
	if err := form.ValidateAnswers(map[string]any{}); err == nil {
		t.Fatal("submitted optional form must validate its required question")
	}
}

func TestAssignmentRejectsTimedTriggerWithoutTime(t *testing.T) {
	assignment := Assignment{Trigger: TriggerAtTime, Audience: Audience{Kind: AudienceAllParticipants}, Presentation: PresentationTask}
	if err := assignment.Validate(); err == nil {
		t.Fatal("timed assignment without at time must be rejected")
	}
}

func TestAssignmentRejectsUnknownCapability(t *testing.T) {
	at := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	assignment := Assignment{
		Trigger: TriggerAtTime, At: &at, Audience: Audience{Kind: AudienceAllParticipants},
		Presentation: PresentationTask, Gates: []Capability{"unknown.action"},
	}
	if err := assignment.Validate(); err == nil {
		t.Fatal("unknown capability must be rejected")
	}
}

func TestEditableFieldsMapsEveryFieldKey(t *testing.T) {
	form := Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City", Editable: true},
		{ID: "school", Type: eventContentModel.BlockField, Key: "school", Input: "text", Label: "School"},
		{ID: "note", Type: "rich_text"},
	}}}
	got := form.EditableFields()
	if len(got) != 2 || !got["city"] || got["school"] {
		t.Fatalf("editable fields = %v", got)
	}
}

func conditionalForm(source eventContentModel.Block, condition eventContentModel.Condition) Form {
	return Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		source,
		{ID: "dependent", Type: eventContentModel.BlockField, Key: "dependent", Input: "text", Label: "Dependent", Condition: &condition},
	}}}
}

func TestFormValidateChecksConditions(t *testing.T) {
	number := eventContentModel.Block{ID: "size", Type: eventContentModel.BlockField, Key: "size", Input: "number", Label: "Size"}
	role := eventContentModel.Block{ID: "role", Type: eventContentModel.BlockField, Key: "role", Input: "select", Label: "Role", Options: []string{"Student", "Other"}}
	langs := eventContentModel.Block{ID: "langs", Type: eventContentModel.BlockField, Key: "langs", Input: "multi_select", Label: "Languages", Options: []string{"Go"}}
	name := eventContentModel.Block{ID: "name", Type: eventContentModel.BlockField, Key: "name", Input: "text", Label: "Name"}
	cases := []struct {
		name  string
		form  Form
		valid bool
	}{
		{"number value", conditionalForm(number, eventContentModel.Condition{FieldKey: "size", Operator: "equals", Value: float64(3)}), true},
		{"number as text", conditionalForm(number, eventContentModel.Condition{FieldKey: "size", Operator: "equals", Value: "3"}), false},
		{"known option", conditionalForm(role, eventContentModel.Condition{FieldKey: "role", Operator: "not_equals", Value: "Other"}), true},
		{"unknown option", conditionalForm(role, eventContentModel.Condition{FieldKey: "role", Operator: "equals", Value: "Teacher"}), false},
		{"multi select source", conditionalForm(langs, eventContentModel.Condition{FieldKey: "langs", Operator: "equals", Value: "Go"}), false},
		{"unknown operator", conditionalForm(role, eventContentModel.Condition{FieldKey: "role", Operator: "contains", Value: "Other"}), false},
		{"empty text value", conditionalForm(name, eventContentModel.Condition{FieldKey: "name", Operator: "equals", Value: " "}), false},
	}
	for _, tc := range cases {
		if err := tc.form.Validate(); (err == nil) != tc.valid {
			t.Fatalf("%s: valid=%v, err=%v", tc.name, tc.valid, err)
		}
	}
}

func TestFormValidateRejectsEmptyAndRepeatedOptions(t *testing.T) {
	for _, options := range [][]string{{"A", " "}, {"A", "A "}} {
		form := Form{Document: eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "a", Type: eventContentModel.BlockField, Key: "a", Input: "select", Label: "A", Options: options}}}}
		if err := form.Validate(); err == nil {
			t.Fatalf("options %q must be rejected", options)
		}
	}
}

func TestFormAnswersTreatUntouchedCheckboxAsNo(t *testing.T) {
	form := conditionalForm(eventContentModel.Block{ID: "team", Type: eventContentModel.BlockField, Key: "team", Input: "checkbox", Label: "Team"}, eventContentModel.Condition{FieldKey: "team", Operator: "equals", Value: false})
	form.Document.Blocks[1].Required = true
	if err := form.ValidateAnswers(map[string]any{}); err == nil {
		t.Fatal("dependent question on an untouched checkbox must be visible and required")
	}
	if err := form.ValidateAnswers(map[string]any{"team": true}); err != nil {
		t.Fatalf("hidden dependent question must not block: %v", err)
	}
}

func TestFormAnswersHideQuestionsDependingOnHiddenOnes(t *testing.T) {
	form := Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "team", Type: eventContentModel.BlockField, Key: "team", Input: "checkbox", Label: "Team"},
		{ID: "size", Type: eventContentModel.BlockField, Key: "size", Input: "number", Label: "Size", Condition: &eventContentModel.Condition{FieldKey: "team", Operator: "equals", Value: true}},
		{ID: "captain", Type: eventContentModel.BlockField, Key: "captain", Input: "text", Label: "Captain", Required: true, Condition: &eventContentModel.Condition{FieldKey: "size", Operator: "not_equals", Value: float64(1)}},
	}}}
	if err := form.ValidateAnswers(map[string]any{"team": false, "size": float64(4)}); err != nil {
		t.Fatalf("question behind a hidden one must stay hidden: %v", err)
	}
	if err := form.ValidateAnswers(map[string]any{"team": true, "size": float64(4)}); err == nil {
		t.Fatal("visible required question must block")
	}
}

func TestValidatePartialAnswersChecksTypesButNotRequired(t *testing.T) {
	form := Form{Enabled: true, Required: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "city", Type: eventContentModel.BlockField, Key: "city", Input: "text", Label: "City", Required: true},
		{ID: "size", Type: eventContentModel.BlockField, Key: "size", Input: "number", Label: "Size"},
		{ID: "level", Type: eventContentModel.BlockField, Key: "level", Input: "select", Label: "Level", Options: []string{"a", "b"}},
		{ID: "cv", Type: eventContentModel.BlockField, Key: "cv", Input: InputFile, Label: "CV"},
	}}}
	if err := form.ValidatePartialAnswers(map[string]any{"size": float64(3), "level": "a"}); err != nil {
		t.Fatalf("missing required field must not block a prefill: %v", err)
	}
	for name, answers := range map[string]map[string]any{
		"wrong type":    {"size": "three"},
		"bad option":    {"level": "z"},
		"file":          {"cv": "x"},
		"unknown field": {"nope": "x"},
	} {
		if err := form.ValidatePartialAnswers(answers); err == nil {
			t.Fatalf("%s must be rejected", name)
		}
	}
}
