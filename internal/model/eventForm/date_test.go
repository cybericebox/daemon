package eventFormModel

import (
	"testing"

	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func dateForm(mode, min, max string, condition *eventContentModel.Condition) Form {
	return Form{Enabled: true, Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "born", Type: eventContentModel.BlockField, Key: "born", Input: InputDate, Label: "Born", Required: true, DateMode: mode, MinDate: min, MaxDate: max},
		{ID: "school", Type: eventContentModel.BlockField, Key: "school", Input: "text", Label: "School", Required: condition != nil, Condition: condition},
	}}}
}

func TestDateQuestionSettings(t *testing.T) {
	valid := []Form{
		dateForm("", "", "", nil),
		dateForm(DateModeDate, "2000-01-01", "2012-12-31", nil),
		dateForm(DateModeDateTime, "2026-10-01T09:00:00Z", "", nil),
		dateForm(DateModeTime, "09:00", "18:30", nil),
	}
	for _, form := range valid {
		if err := form.Validate(); err != nil {
			t.Fatalf("valid date question refused: %v", err)
		}
	}
	for _, form := range []Form{
		dateForm("week", "", "", nil),
		dateForm("none", "", "", nil),
		dateForm(DateModeTime, "9:00", "", nil),
		dateForm(DateModeTime, "18:00", "09:00", nil),
		dateForm(DateModeDate, "01.01.2000", "", nil),
		dateForm(DateModeDateTime, "2026-10-01T09:00:00+03:00", "", nil),
		dateForm(DateModeDate, "2012-01-01", "2000-01-01", nil),
	} {
		if err := form.Validate(); err == nil {
			t.Fatalf("invalid date settings accepted: %+v", form.Document.Blocks[0])
		}
	}
}

func TestDateAnswersMatchTheMode(t *testing.T) {
	form := dateForm(DateModeDate, "2000-01-01", "2012-12-31", nil)
	if err := form.ValidateAnswers(map[string]any{"born": "2005-02-28"}); err != nil {
		t.Fatalf("valid date refused: %v", err)
	}
	for _, bad := range []any{"2005-02-30", "2005-02-28T00:00:00Z", "1999-12-31", "2013-01-01", 20050228} {
		if err := form.ValidateAnswers(map[string]any{"born": bad}); err == nil {
			t.Fatalf("date answer %v accepted", bad)
		}
	}
	form = dateForm(DateModeDateTime, "", "", nil)
	if err := form.ValidateAnswers(map[string]any{"born": "2026-09-29T12:30:00.000Z"}); err != nil {
		t.Fatalf("UTC datetime refused: %v", err)
	}
	for _, bad := range []any{"2026-09-29", "2026-09-29T12:30:00+03:00", "2026-09-29 12:30"} {
		if err := form.ValidateAnswers(map[string]any{"born": bad}); err == nil {
			t.Fatalf("datetime answer %v accepted", bad)
		}
	}
}

func TestDateConditions(t *testing.T) {
	before := &eventContentModel.Condition{FieldKey: "born", Operator: OperatorBefore, Value: "2008-01-01"}
	form := dateForm(DateModeDate, "", "", before)
	if err := form.Validate(); err != nil {
		t.Fatalf("before on a date: %v", err)
	}
	if err := form.ValidateAnswers(map[string]any{"born": "2005-05-05"}); err == nil {
		t.Fatal("an earlier date shows the question, so it is required")
	}
	if err := form.ValidateAnswers(map[string]any{"born": "2010-05-05"}); err != nil {
		t.Fatalf("a later date hides the question: %v", err)
	}
	after := &eventContentModel.Condition{FieldKey: "born", Operator: OperatorAfter, Value: "2026-09-29T12:00:00Z"}
	form = dateForm(DateModeDateTime, "", "", after)
	if err := form.ValidateAnswers(map[string]any{"born": "2026-09-29T12:00:01Z"}); err == nil {
		t.Fatal("a later time shows the question")
	}
	for _, condition := range []*eventContentModel.Condition{
		{FieldKey: "born", Operator: OperatorBefore, Value: "tomorrow"},
		{FieldKey: "born", Operator: "contains", Value: "2008-01-01"},
	} {
		if err := dateForm(DateModeDate, "", "", condition).Validate(); err == nil {
			t.Fatalf("condition %+v accepted", condition)
		}
	}
	text := Form{Document: eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "a", Type: eventContentModel.BlockField, Key: "a", Input: "text", Label: "A"},
		{ID: "b", Type: eventContentModel.BlockField, Key: "b", Input: "text", Label: "B", Condition: &eventContentModel.Condition{FieldKey: "a", Operator: OperatorBefore, Value: "x"}},
	}}}
	if err := text.Validate(); err == nil {
		t.Fatal("before/after apply only to dates")
	}
}

func TestTimeAnswersAndConditions(t *testing.T) {
	form := dateForm(DateModeTime, "09:00", "18:00", nil)
	if err := form.ValidateAnswers(map[string]any{"born": "12:30"}); err != nil {
		t.Fatalf("valid time refused: %v", err)
	}
	for _, bad := range []any{"24:00", "12:60", "8:30", "12:30:00", "2026-09-29", "08:59", "18:01"} {
		if err := form.ValidateAnswers(map[string]any{"born": bad}); err == nil {
			t.Fatalf("time answer %v accepted", bad)
		}
	}
	form = dateForm(DateModeTime, "", "", &eventContentModel.Condition{FieldKey: "born", Operator: OperatorAfter, Value: "17:00"})
	if err := form.ValidateAnswers(map[string]any{"born": "17:30"}); err == nil {
		t.Fatal("a later time of day shows the question")
	}
	if err := form.ValidateAnswers(map[string]any{"born": "16:00"}); err != nil {
		t.Fatalf("an earlier time of day hides it: %v", err)
	}
}
