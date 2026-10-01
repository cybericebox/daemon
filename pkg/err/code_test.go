package err_test

import (
	"errors"
	"testing"

	liberr "github.com/cybericebox/daemon/pkg/err"
)

// errors.Is routes through statusCode.As. With a fully-specified target it
// must compare the (objectCode, detailCode) pair — comparing only detailCode
// conflates unrelated errors from different objects (e.g. a users 404 with an
// auth 409 that happen to share a detail code).
func TestErrorsIs_DetailCodeScopedToObject(t *testing.T) {
	authNotFound := liberr.ErrObjectNotFound.WithObjectCode(10).WithDetailCode(3)
	userExists := liberr.ErrObjectExists.WithObjectCode(20).WithDetailCode(3)

	if errors.Is(authNotFound.Err(), userExists.Err()) {
		t.Fatal("same detailCode under different objectCodes must NOT match")
	}
	if !errors.Is(
		authNotFound.Err(),
		liberr.ErrObjectNotFound.WithObjectCode(10).WithDetailCode(3).Err(),
	) {
		t.Fatal("identical (objectCode, detailCode) must match")
	}
}

func TestErrorsIs_SameObjectDifferentDetail(t *testing.T) {
	a := liberr.ErrInvalidData.WithObjectCode(10).WithDetailCode(1).Err()
	b := liberr.ErrInvalidData.WithObjectCode(10).WithDetailCode(2).Err()
	if errors.Is(a, b) || errors.Is(b, a) {
		t.Fatal("different detailCodes must not match")
	}
}

// A target built without an objectCode still matches by detailCode alone —
// callers that never set object codes keep their old behavior.
func TestErrorsIs_DetailOnlyTargetKeepsLegacyBehavior(t *testing.T) {
	actual := liberr.ErrInvalidData.WithObjectCode(10).WithDetailCode(7).Err()
	target := liberr.ErrInvalidData.WithDetailCode(7).Err()
	if !errors.Is(actual, target) {
		t.Fatal("detail-only target must match on detailCode")
	}
}

func TestErrorsIs_ObjectAndInformLevels(t *testing.T) {
	actual := liberr.ErrInvalidData.WithObjectCode(10).WithDetailCode(7).Err()
	if !errors.Is(actual, liberr.ErrInvalidData.WithObjectCode(10).Err()) {
		t.Fatal("object-level target must match any detail within the object")
	}
	if errors.Is(actual, liberr.ErrInvalidData.WithObjectCode(20).Err()) {
		t.Fatal("object-level target must not match a different object")
	}
	if !errors.Is(actual, liberr.ErrInvalidData.Err()) {
		t.Fatal("inform-level target must match by inform code")
	}
}
