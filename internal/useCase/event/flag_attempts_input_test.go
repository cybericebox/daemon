package event

import (
	"encoding/json"
	"testing"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

func TestOptionalLimitTellsOmittedFromNull(t *testing.T) {
	var body struct {
		Limit OptionalLimit `json:"Limit"`
	}
	five := int32(5)
	for name, tc := range map[string]struct {
		json string
		set  bool
		want *int32
	}{
		"omitted": {`{}`, false, nil},
		"null":    {`{"Limit":null}`, true, nil},
		"number":  {`{"Limit":5}`, true, &five},
	} {
		body.Limit = OptionalLimit{}
		if err := json.Unmarshal([]byte(tc.json), &body); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if body.Limit.Set != tc.set || (body.Limit.Value == nil) != (tc.want == nil) || (tc.want != nil && *body.Limit.Value != *tc.want) {
			t.Fatalf("%s: %+v", name, body.Limit)
		}
	}
	if err := json.Unmarshal([]byte(`{"Limit":"x"}`), &body); err == nil {
		t.Fatal("a non-number is refused")
	}
}

func TestUpdateConfigInputMaxFlagAttemptsMerge(t *testing.T) {
	four := int32(4)
	current := eventConfigModel.EventConfig{MaxFlagAttempts: &four}
	if got := (UpdateConfigInput{}).toConfigInput(current).MaxFlagAttempts; got == nil || *got != 4 {
		t.Fatalf("omitted keeps the limit, got %v", got)
	}
	if got := (UpdateConfigInput{MaxFlagAttempts: OptionalLimit{Set: true}}).toConfigInput(current).MaxFlagAttempts; got != nil {
		t.Fatalf("null clears the limit, got %v", *got)
	}
	two := int32(2)
	if got := (UpdateConfigInput{MaxFlagAttempts: OptionalLimit{Set: true, Value: &two}}).toConfigInput(current).MaxFlagAttempts; got == nil || *got != 2 {
		t.Fatalf("a number replaces the limit, got %v", got)
	}
}
