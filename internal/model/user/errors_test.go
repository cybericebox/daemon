package userModel_test

import (
	"errors"
	"testing"

	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// ErrUserNotFound (404) and ErrUserExists (409-family) shared DetailCode 1,
// and the lib's err.As compares only DetailCode — errors.Is conflated them.
func TestUserErrorCodes_NoConflation(t *testing.T) {
	if errors.Is(userModel.ErrUserNotFound.Err(), userModel.ErrUserExists.Err()) {
		t.Fatal("ErrUserNotFound must not match ErrUserExists")
	}
	if errors.Is(userModel.ErrUserExists.Err(), userModel.ErrUserNotFound.Err()) {
		t.Fatal("ErrUserExists must not match ErrUserNotFound")
	}
}
