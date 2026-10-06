package main

import "testing"

func TestScan_FindsDuplicateWithinObject(t *testing.T) {
	src := `
var (
	ErrA = err.ErrUnauthenticated.WithObjectCode(model.AuthObjectCode).
		WithMessage("a").WithDetailCode(3)
	ErrB = err.ErrObjectNotFound.WithObjectCode(model.AuthObjectCode).
		WithMessage("b").WithDetailCode(3)
	ErrC = err.ErrInvalidData.WithObjectCode(model.UserObjectCode).
		WithMessage("c").WithDetailCode(3)
)`
	found := scan(src)
	if len(found["AuthObjectCode:3"]) != 2 {
		t.Fatalf("want 2 declarations of AuthObjectCode:3, got %d", len(found["AuthObjectCode:3"]))
	}
	// Same DetailCode under a different object is NOT a duplicate.
	if len(found["UserObjectCode:3"]) != 1 {
		t.Fatalf("want 1 declaration of UserObjectCode:3, got %d", len(found["UserObjectCode:3"]))
	}
}
