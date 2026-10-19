package eventContentModel

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBlockLayoutRoundTrip(t *testing.T) {
	source := `{"blocks":[{"id":"banner","type":"banner","spacing":{"desktop":{"top":0,"bottom":24,"inline":48},"mobile":{"inline":0}},"maxWidth":960,"contentAlign":"center","mobileFullWidth":true}]}`
	var doc Document
	if err := json.Unmarshal([]byte(source), &doc); err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"top":0`, `"inline":0`, `"maxWidth":960`, `"mobileFullWidth":true`} {
		if !strings.Contains(string(result), field) {
			t.Errorf("lost layout field %s: %s", field, result)
		}
	}
}

func TestBlockLayoutRejectsInvalidValues(t *testing.T) {
	for _, property := range []string{`"spacing":{"desktop":{"top":-1}}`, `"spacing":{"mobile":{"inline":161}}`, `"maxWidth":0`, `"maxWidth":1921`, `"contentAlign":"diagonal"`, `"textColumns":3`, `"textColumns":2`} {
		t.Run(property, func(t *testing.T) {
			var doc Document
			if err := json.Unmarshal([]byte(`{"blocks":[{"id":"banner","type":"banner",`+property+`}]}`), &doc); err != nil {
				t.Fatal(err)
			}
			if err := doc.Validate(); err == nil {
				t.Fatal("expected invalid layout to be rejected")
			}
		})
	}
}
