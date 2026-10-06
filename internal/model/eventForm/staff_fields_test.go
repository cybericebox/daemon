package eventFormModel

import (
	"reflect"
	"testing"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func field(key, input string, mutate func(*eventContentModel.Block)) eventContentModel.Block {
	block := eventContentModel.Block{ID: key, Type: eventContentModel.BlockField, Key: key, Input: input, Label: key}
	if mutate != nil {
		mutate(&block)
	}
	return block
}

func staffForm() Form {
	return Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		field("city", "text", func(b *eventContentModel.Block) { b.Required = true }),
		field("note", "long_text", func(b *eventContentModel.Block) { b.StaffOnly = true; b.Required = true }),
		field("came", "checkbox", func(b *eventContentModel.Block) { b.StaffOnly = true }),
	}}}
}

func TestStaffOnlyFieldIsNeverRequiredFromParticipants(t *testing.T) {
	form := staffForm()
	if err := form.ValidateAnswers(map[string]any{"city": "Kyiv"}); err != nil {
		t.Fatalf("staff-only required field must not block a participant: %v", err)
	}
	if got := form.MissingRequired(map[string]any{"city": "Kyiv"}); len(got) != 0 {
		t.Fatalf("missing = %v, want none", got)
	}
}

func TestMissingRequiredListsEmptyVisibleQuestions(t *testing.T) {
	form := Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		field("student", "checkbox", nil),
		field("university", "text", func(b *eventContentModel.Block) {
			b.Required = true
			b.Condition = &eventContentModel.Condition{FieldKey: "student", Operator: "equals", Value: true}
		}),
		field("city", "text", func(b *eventContentModel.Block) { b.Required = true }),
		field("age", "number", func(b *eventContentModel.Block) { b.Required = true }),
	}}}
	if got := form.MissingRequired(map[string]any{"student": false, "age": 0.0}); !reflect.DeepEqual(got, []string{"city"}) {
		t.Fatalf("hidden question counted or filled one missed: %v", got)
	}
	if got := form.MissingRequired(map[string]any{"student": true, "city": ""}); !reflect.DeepEqual(got, []string{"university", "city", "age"}) {
		t.Fatalf("missing = %v", got)
	}
	if got := form.MissingRequired(nil); !reflect.DeepEqual(got, []string{"city", "age"}) {
		t.Fatalf("no answers: missing = %v", got)
	}
}

func TestForParticipantDropsStaffQuestionsWithoutTouchingTheOriginal(t *testing.T) {
	form := staffForm()
	visible := form.ForParticipant()
	if len(visible.Document.Blocks) != 1 || visible.Document.Blocks[0].Key != "city" {
		t.Fatalf("participant blocks = %+v", visible.Document.Blocks)
	}
	if len(form.Document.Blocks) != 3 {
		t.Fatal("ForParticipant modified the original form")
	}
}

func TestKeepStaffAnswersProtectsRecordedValues(t *testing.T) {
	form := staffForm()
	stored := map[string]any{"city": "Old", "note": "VIP", "came": true}
	saved := form.KeepStaffAnswers(stored, map[string]any{"city": "Lviv", "note": "hacked", "came": false})
	want := map[string]any{"city": "Lviv", "note": "VIP", "came": true}
	if !reflect.DeepEqual(saved, want) {
		t.Fatalf("saved = %v, want %v", saved, want)
	}
	if got := form.WithoutStaffAnswers(stored); !reflect.DeepEqual(got, map[string]any{"city": "Old"}) {
		t.Fatalf("participant answers = %v", got)
	}
}

func TestValidateStaffAnswersOnlyAcceptsStaffQuestions(t *testing.T) {
	form := staffForm()
	if err := form.ValidateStaffAnswers(map[string]any{"note": "ok", "came": true}); err != nil {
		t.Fatal(err)
	}
	if err := form.ValidateStaffAnswers(map[string]any{"city": "x"}); err == nil {
		t.Fatal("a participant question must be refused")
	}
	if err := form.ValidateStaffAnswers(map[string]any{"came": "yes"}); err == nil {
		t.Fatal("a wrong type must be refused")
	}
}

func TestValidateRejectsParticipantFieldDependingOnStaffField(t *testing.T) {
	form := Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		field("came", "checkbox", func(b *eventContentModel.Block) { b.StaffOnly = true }),
		field("why", "text", func(b *eventContentModel.Block) {
			b.Condition = &eventContentModel.Condition{FieldKey: "came", Operator: "equals", Value: true}
		}),
	}}}
	if err := form.Validate(); err == nil {
		t.Fatal("a participant field must not depend on a staff-only field")
	}
	form.Document.Blocks[1].StaffOnly = true
	if err := form.Validate(); err != nil {
		t.Fatalf("staff-only field may depend on a staff-only field: %v", err)
	}
	form.Document.Blocks[0].Input = "file"
	form.Document.Blocks[0].FileTypes = []string{"pdf"}
	form.Document.Blocks[0].MaxSizeMB = 1
	if err := form.Validate(); err == nil {
		t.Fatal("a staff-only file field must be refused")
	}
}
