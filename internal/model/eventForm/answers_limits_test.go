package eventFormModel

import (
	"strings"
	"testing"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func limitsForm() Form {
	return Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "name", Type: eventContentModel.BlockField, Key: "name", Input: "text", Label: "Name", Required: true},
		{ID: "bio", Type: eventContentModel.BlockField, Key: "bio", Input: "long_text", Label: "Bio"},
		{ID: "likes", Type: eventContentModel.BlockField, Key: "likes", Input: "multi_select", Label: "Likes", Options: []string{"a", "b"}},
		{ID: "student", Type: eventContentModel.BlockField, Key: "student", Input: "checkbox", Label: "Student"},
		{ID: "school", Type: eventContentModel.BlockField, Key: "school", Input: "text", Label: "School",
			Condition: &eventContentModel.Condition{FieldKey: "student", Operator: "equals", Value: true}},
	}}}
}

// The reported PoC: {"name": "<5 MB>", "evil": {"deep": ["<img onerror>"]}} was accepted and stored.
func TestAnswersAreBoundedAndOnlyDeclaredFieldsAreKept(t *testing.T) {
	form := limitsForm()
	ok := map[string]any{"name": "Ann", "bio": strings.Repeat("b", MaxLongTextAnswerBytes), "likes": []any{"a", "b"}}
	if err := form.ValidateAnswers(ok); err != nil {
		t.Fatalf("answers within the limits: %v", err)
	}
	for name, answers := range map[string]map[string]any{
		"unknown key":             {"name": "Ann", "evil": map[string]any{"deep": []any{"<img onerror>"}}},
		"huge one-line answer":    {"name": strings.Repeat("x", MaxTextAnswerBytes+1)},
		"huge long answer":        {"name": "Ann", "bio": strings.Repeat("x", MaxLongTextAnswerBytes+1)},
		"more picks than options": {"name": "Ann", "likes": []any{"a", "a", "a"}},
		"huge hidden answer":      {"name": "Ann", "school": strings.Repeat("x", MaxTextAnswerBytes+1)},
		"wrong type when hidden":  {"name": "Ann", "school": map[string]any{"x": 1}},
	} {
		if err := form.ValidateAnswers(answers); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// A hidden question is not demanded, and a well-formed answer for it is tolerated.
	if err := form.ValidateAnswers(map[string]any{"name": "Ann", "school": "KPI"}); err != nil {
		t.Errorf("a valid answer to a hidden question: %v", err)
	}
	// Many valid answers together still stay under the total cap.
	many := map[string]any{"name": "Ann", "bio": strings.Repeat("b", MaxLongTextAnswerBytes), "school": strings.Repeat("s", MaxTextAnswerBytes)}
	if err := form.ValidateAnswers(many); err != nil {
		t.Errorf("answers within the total: %v", err)
	}
}
