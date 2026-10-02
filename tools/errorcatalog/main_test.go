package main

import (
	"os"
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
