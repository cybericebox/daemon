package exercise

import (
	"encoding/json"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestNormalizeContentIDs_AssignsStablePlaceholderKeys(t *testing.T) {
	variants := []exerciseModel.Variant{{Tasks: []exerciseModel.Task{{Placeholders: []exerciseModel.Placeholder{{Kind: exerciseModel.PlaceholderIP, IPReference: "static"}}}}}}
	normalizeContentIDs(variants)
	key := variants[0].Tasks[0].Placeholders[0].Key
	if len(key) < 6 || key[:3] != "ph_" {
		t.Fatalf("missing stable placeholder key: %q", key)
	}
	normalizeContentIDs(variants)
	if variants[0].Tasks[0].Placeholders[0].Key != key {
		t.Fatal("normalization replaced an existing placeholder key")
	}
	encoded, err := json.Marshal(variants)
	if err != nil {
		t.Fatal(err)
	}
	var reopened []exerciseModel.Variant
	if err := json.Unmarshal(encoded, &reopened); err != nil {
		t.Fatal(err)
	}
	if reopened[0].Tasks[0].Placeholders[0].Key != key {
		t.Fatal("placeholder key disappeared when reopening a version")
	}
}
