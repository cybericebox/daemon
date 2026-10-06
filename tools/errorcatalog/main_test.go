package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestCatalogUpToDate fails when error-catalog/errors.en.json differs from a fresh
// generation; fix it with `make error-catalog`.
func TestCatalogUpToDate(t *testing.T) {
	got, err := generate("../../internal/model", "../../pkg/ipam")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("../../error-catalog/errors.en.json")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatal("error-catalog/errors.en.json is stale: run `make error-catalog` and commit the result")
	}
}

func loadCatalog(t *testing.T, name string) map[string]string {
	t.Helper()
	data, err := os.ReadFile("../../error-catalog/" + name)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]string{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// TestUkrainianCatalogComplete fails when a code has no Ukrainian text (or the
// uk file keeps a code the backend no longer has), or uk uses an ASCII apostrophe.
func TestUkrainianCatalogComplete(t *testing.T) {
	en, uk := loadCatalog(t, "errors.en.json"), loadCatalog(t, "errors.uk.json")
	for code := range en {
		if _, ok := uk[code]; !ok {
			t.Errorf("errors.uk.json has no text for code %s: add it", code)
		}
	}
	for code, msg := range uk {
		if _, ok := en[code]; !ok {
			t.Errorf("errors.uk.json has code %s that is not in the catalog: remove it", code)
		}
		if strings.ContainsAny(msg, "'’") {
			t.Errorf("errors.uk.json code %s: use ʼ (U+02BC) as the apostrophe", code)
		}
	}
}
