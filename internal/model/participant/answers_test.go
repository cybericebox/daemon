package participantModel_test

import (
	"errors"
	"testing"
	"time"

	participantModel "github.com/cybericebox/daemon/internal/model/participant"
)

func TestMergeEditableAnswers(t *testing.T) {
	editable := map[string]bool{"city": true, "school": false}
	stored := map[string]any{"city": "Kyiv", "school": "KPI"}

	merged, err := participantModel.MergeEditableAnswers(editable, stored, map[string]any{"city": "Lviv", "school": "KPI"})
	if err != nil || merged["city"] != "Lviv" || merged["school"] != "KPI" {
		t.Fatalf("editable change: %v, %v", merged, err)
	}
	if stored["city"] != "Kyiv" {
		t.Fatal("stored answers must not be mutated")
	}
	merged, err = participantModel.MergeEditableAnswers(editable, stored, map[string]any{"city": "Odesa"})
	if err != nil || merged["school"] != "KPI" || merged["city"] != "Odesa" {
		t.Fatalf("omitted keys keep stored values: %v, %v", merged, err)
	}
	for name, submitted := range map[string]map[string]any{
		"non-editable change": {"school": "LNU"},
		"unknown key":         {"nickname": "x"},
		"non-editable added":  {"school": nil},
	} {
		if _, err = participantModel.MergeEditableAnswers(editable, stored, submitted); !errors.Is(err, participantModel.ErrParticipantFieldNotEditable.Err()) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if _, err = participantModel.MergeEditableAnswers(editable, map[string]any{}, map[string]any{"school": nil}); err != nil {
		t.Fatalf("an absent non-editable key submitted as null is unchanged: %v", err)
	}
}

func TestAnswersEditableUntilEffectiveFinish(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	later, earlier := now.Add(time.Minute), now.Add(-time.Minute)
	if !participantModel.AnswersEditable(nil, now) || !participantModel.AnswersEditable(&later, now) {
		t.Fatal("answers are editable before the effective finish")
	}
	if participantModel.AnswersEditable(&earlier, now) || participantModel.AnswersEditable(&now, now) {
		t.Fatal("answers lock at the effective finish")
	}
}

func TestLockFilledAnswers(t *testing.T) {
	editable := map[string]bool{"city": true, "school": false, "group": false}
	stored := map[string]any{"city": "Kyiv", "school": "KPI", "group": ""}

	got, err := participantModel.LockFilledAnswers(editable, stored, map[string]any{"city": "Lviv", "group": "A"})
	if err != nil || got["city"] != "Lviv" || got["school"] != "KPI" || got["group"] != "A" {
		t.Fatalf("filled non-editable kept, empty one fillable: %v %v", got, err)
	}
	if _, err = participantModel.LockFilledAnswers(editable, stored, map[string]any{"school": "LNU"}); !errors.Is(err, participantModel.ErrParticipantFieldPrefilled.Err()) {
		t.Fatalf("changing a filled non-editable field must fail: %v", err)
	}
	if _, err = participantModel.LockFilledAnswers(editable, stored, map[string]any{"school": "KPI"}); err != nil {
		t.Fatalf("resubmitting the same value is fine: %v", err)
	}
}
