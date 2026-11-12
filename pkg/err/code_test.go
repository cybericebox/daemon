package err_test

import (
	"errors"
	"strings"
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

func TestPublicContextIsOnlyWhatWasMarkedAndNeverShared(t *testing.T) {
	base := liberr.ErrConflict
	e := base.WithContext("agent", "eu").WithPublicContext("from", "x").Err()
	if got := e.PublicContext(); len(got) != 1 || got["from"] != "x" {
		t.Fatalf("public = %v", got)
	}
	got := e.PublicContext()
	got["agent"] = "leak"
	if _, leaked := e.PublicContext()["agent"]; leaked {
		t.Fatal("the returned map must be a copy")
	}
	if other := base.Err().PublicContext(); other != nil {
		t.Fatalf("the shared base error gained context: %v", other)
	}
	if !strings.Contains(e.Error(), "AGENT:=eu") || !strings.Contains(e.Error(), "FROM:=x") {
		t.Fatalf("both stay in the log line: %s", e.Error())
	}
}

// AddPublicContext puts a public value on an error that was built earlier; the error keeps its code, a value that is
// already there wins, and what is not an application error is returned as it is.
func TestAddPublicContext(t *testing.T) {
	publicOf := func(failure error) map[string]any {
		var coded liberr.Error
		if !errors.As(failure, &coded) {
			t.Fatalf("not an application error: %v", failure)
		}
		return coded.PublicContext()
	}
	built := liberr.ErrInvalidData.WithObjectCode(10).WithDetailCode(7).WithPublicContext("task", "A").Err()
	got := liberr.AddPublicContext(built, "variant", 2)
	if !errors.Is(got, built) {
		t.Fatal("the code must stay")
	}
	if public := publicOf(got); public["variant"] != 2 || public["task"] != "A" {
		t.Fatalf("public context = %v", public)
	}
	if _, has := publicOf(built)["variant"]; has {
		t.Fatal("the original error must not change")
	}
	if again := publicOf(liberr.AddPublicContext(got, "variant", 9)); again["variant"] != 2 {
		t.Fatalf("an existing value must win: %v", again)
	}
	plain := errors.New("plain")
	if !errors.Is(liberr.AddPublicContext(plain, "variant", 1), plain) || liberr.AddPublicContext(nil, "variant", 1) != nil {
		t.Fatal("not an application error: returned as it is")
	}
}
