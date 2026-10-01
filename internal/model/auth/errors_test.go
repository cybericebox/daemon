package authModel_test

import (
	"errors"
	"testing"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

// The lib's err.As compares only DetailCode when the target has one, so two
// error vars sharing a DetailCode inside one object are conflated by
// errors.Is. These pairs need distinct HTTP statuses (401 vs 404), so
// conflation is a real routing bug, not a style issue.
func TestAuthErrorCodes_NoConflation(t *testing.T) {
	if errors.Is(authModel.ErrAuthInvalidSession.Err(), authModel.ErrAuthSessionNotFound.Err()) {
		t.Fatal("ErrAuthInvalidSession (401) must not match ErrAuthSessionNotFound (404)")
	}
	if errors.Is(authModel.ErrAuthSessionNotFound.Err(), authModel.ErrAuthInvalidSession.Err()) {
		t.Fatal("ErrAuthSessionNotFound (404) must not match ErrAuthInvalidSession (401)")
	}
}
