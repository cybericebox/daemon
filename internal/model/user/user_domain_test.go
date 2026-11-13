package userModel_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

var now = time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

var bothDocs = []string{"terms", "privacy"}

func incompleteUser() userModel.User {
	return userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "new@test.test", now.Add(-time.Hour))
}

func TestNewIncompleteUser_Defaults(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	u := userModel.NewIncompleteUser(id, "a@b.test", now)

	if u.ID != id || u.Email != "a@b.test" {
		t.Fatalf("identity not set: %+v", u)
	}
	if u.Status != userModel.UserStatusIncomplete || u.Role != rbac.RoleUser {
		t.Fatalf("want incomplete/user defaults, got %s/%s", u.Status, u.Role)
	}
	if !u.CreatedAt.Equal(now) || !u.UpdatedAt.Equal(now) {
		t.Fatalf("timestamps must default to now: %+v", u)
	}
	if u.EmailConfirmed || u.HasPassword() {
		t.Fatal("fresh incomplete user must have no confirmation and no password")
	}
}

func TestCompleteSetup_HappyPath(t *testing.T) {
	u := incompleteUser()

	err := u.CompleteSetup("Ivan", "Test", "hashed-pwd", bothDocs, bothDocs, hasProviderStub(false), now)
	if err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}
	if u.Status != userModel.UserStatusActive || !u.EmailConfirmed {
		t.Fatalf("must become active+confirmed: %+v", u)
	}
	if u.FirstName != "Ivan" || u.LastName != "Test" || !u.HasPassword() {
		t.Fatalf("profile/password not applied: %+v", u)
	}
	if !reflect.DeepEqual(u.LegalDocuments, bothDocs) || u.TosAcceptedAt == nil || !u.TosAcceptedAt.Equal(now) {
		t.Fatalf("ToS not recorded: %+v", u)
	}
	if !u.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt not touched: %v", u.UpdatedAt)
	}
}

func TestCompleteSetup_Invariants(t *testing.T) {
	active := incompleteUser()
	_ = active.CompleteSetup("a", "b", "pwd", bothDocs, bothDocs, hasProviderStub(false), now)
	if err := active.CompleteSetup("x", "y", "pwd", bothDocs, bothDocs, hasProviderStub(false), now); !errors.Is(err, authModel.ErrSetupAlreadyComplete.Err()) {
		t.Fatalf("second setup: want ErrSetupAlreadyComplete, got %v", err)
	}

	u := incompleteUser()
	if err := u.CompleteSetup("a", "b", "pwd", nil, bothDocs, hasProviderStub(false), now); !errors.Is(err, authModel.ErrTosNotAccepted.Err()) {
		t.Fatalf("nothing accepted: want ErrTosNotAccepted, got %v", err)
	}

	u = incompleteUser()
	if err := u.CompleteSetup("a", "b", "", bothDocs, bothDocs, hasProviderStub(false), now); !errors.Is(err, authModel.ErrNoLoginMethod.Err()) {
		t.Fatalf("no password, no provider: want ErrNoLoginMethod, got %v", err)
	}
	// provider-linked account may finish without a password
	u = incompleteUser()
	if err := u.CompleteSetup("a", "b", "", bothDocs, bothDocs, hasProviderStub(true), now); err != nil {
		t.Fatalf("provider-only setup must pass: %v", err)
	}
	if u.HasPassword() {
		t.Fatal("no password expected")
	}
}

func TestBlockActivate_Transitions(t *testing.T) {
	u := incompleteUser()
	_ = u.CompleteSetup("a", "b", "pwd", bothDocs, bothDocs, hasProviderStub(false), now)

	if err := u.Block(now); err != nil || u.Status != userModel.UserStatusBlocked {
		t.Fatalf("active→blocked must pass: %v %s", err, u.Status)
	}
	if err := u.Activate(now); err != nil || u.Status != userModel.UserStatusActive {
		t.Fatalf("blocked→active must pass: %v %s", err, u.Status)
	}

	inc := incompleteUser()
	if err := inc.Block(now); !errors.Is(err, authModel.ErrInvalidUserStatus.Err()) {
		t.Fatalf("incomplete cannot be blocked: got %v", err)
	}

	del := incompleteUser()
	_ = del.CompleteSetup("a", "b", "pwd", bothDocs, bothDocs, hasProviderStub(false), now)
	_ = del.SoftDelete(now)
	if err := del.Activate(now); !errors.Is(err, authModel.ErrInvalidUserStatus.Err()) {
		t.Fatalf("deleted cannot be activated: got %v", err)
	}
}

func TestSoftDelete_ScrubsPIIAndIsNotRepeatable(t *testing.T) {
	u := incompleteUser()
	_ = u.CompleteSetup("Ivan", "Test", "pwd", bothDocs, bothDocs, hasProviderStub(false), now)
	id := u.ID

	if err := u.SoftDelete(now); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if u.Status != userModel.UserStatusDeleted || u.DeletedAt == nil || !u.DeletedAt.Equal(now) {
		t.Fatalf("not marked deleted: %+v", u)
	}
	if u.Email != "deleted+"+id.String()+"@deleted.local" {
		t.Fatalf("email not scrubbed: %q", u.Email)
	}
	if u.FirstName != "" || u.LastName != "" || u.Picture != "" || u.HasPassword() {
		t.Fatalf("PII not scrubbed: %+v", u)
	}
	if err := u.SoftDelete(now); !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		t.Fatalf("second delete: want ErrUserNotFound, got %v", err)
	}
}

func TestChangeRole_ValidatesRole(t *testing.T) {
	u := incompleteUser()
	_ = u.CompleteSetup("a", "b", "pwd", bothDocs, bothDocs, hasProviderStub(false), now)

	if err := u.ChangeRole(rbac.RoleAdmin, now); err != nil || u.Role != rbac.RoleAdmin {
		t.Fatalf("valid role must apply: %v %s", err, u.Role)
	}
	if err := u.ChangeRole("ghost", now); !errors.Is(err, authModel.ErrInvalidRole.Err()) {
		t.Fatalf("invalid role: want ErrInvalidRole, got %v", err)
	}
}

func TestSmallMutators_TouchUpdatedAt(t *testing.T) {
	u := incompleteUser()
	_ = u.CompleteSetup("a", "b", "pwd", bothDocs, bothDocs, hasProviderStub(false), now.Add(-time.Minute))

	u.UpdateProfile("New", "Name", now)
	if u.FirstName != "New" || !u.UpdatedAt.Equal(now) {
		t.Fatalf("UpdateProfile: %+v", u)
	}

	later := now.Add(time.Minute)
	u.SetPassword("new-hash", later)
	if u.HashedPassword != "new-hash" || !u.UpdatedAt.Equal(later) {
		t.Fatalf("SetPassword: %+v", u)
	}

	u.ChangeEmail("x@y.test", later)
	if u.Email != "x@y.test" {
		t.Fatalf("ChangeEmail: %+v", u)
	}

	u.SetPicture("avatars/x.webp", later)
	if u.Picture != "avatars/x.webp" {
		t.Fatalf("SetPicture: %+v", u)
	}

	u.ConfirmEmail(later)
	if !u.EmailConfirmed {
		t.Fatalf("ConfirmEmail: %+v", u)
	}
}

func TestStatusPredicates(t *testing.T) {
	u := incompleteUser()
	if !u.IsIncomplete() || u.IsBlocked() || u.IsDeleted() {
		t.Fatalf("fresh user predicates wrong: %+v", u)
	}
	_ = u.CompleteSetup("a", "b", "pwd", bothDocs, bothDocs, hasProviderStub(false), now)
	_ = u.Block(now)
	if !u.IsBlocked() {
		t.Fatal("IsBlocked after Block")
	}
}

func TestNewInvitedUser_EmailPreconfirmed(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	u := userModel.NewInvitedUser(id, "inv@test.test", "In", "Vitee", rbac.RoleAdminViewer, now)

	if u.Status != userModel.UserStatusIncomplete {
		t.Fatalf("invited user starts incomplete, got %s", u.Status)
	}
	if !u.EmailConfirmed {
		t.Fatal("invitation is admin-authorized — email must be pre-confirmed")
	}
	if u.Role != rbac.RoleAdminViewer || u.FirstName != "In" || u.LastName != "Vitee" {
		t.Fatalf("invite fields not applied: %+v", u)
	}
}

func TestNewGoogleUser_EmailConfirmedByProvider(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	u := userModel.NewGoogleUser(id, "g@test.test", "Goo", "Gle", now)

	if u.Status != userModel.UserStatusIncomplete || u.Role != rbac.RoleUser {
		t.Fatalf("google user starts incomplete/user: %+v", u)
	}
	if !u.EmailConfirmed {
		t.Fatal("provider-verified email must be confirmed")
	}
	if u.FirstName != "Goo" || u.LastName != "Gle" {
		t.Fatalf("provider first/last name not applied: %+v", u)
	}
}

// hasProviderStub returns a lazy provider-lookup with a fixed answer.
func hasProviderStub(linked bool) func() (bool, error) {
	return func() (bool, error) { return linked, nil }
}

func TestSeenAfter_ComparesLastSeen(t *testing.T) {
	u := incompleteUser()
	u.LastSeen = now
	if !u.SeenAfter(now.Add(-time.Second)) {
		t.Fatal("seen after an earlier warning")
	}
	if u.SeenAfter(now) || u.SeenAfter(now.Add(time.Second)) {
		t.Fatal("not seen after a warning at or after last_seen")
	}
}

// An account created by an invitation may have no name; finishing registration needs both.
func TestCompleteSetup_RequiresNames(t *testing.T) {
	for _, names := range [][2]string{{"", "Test"}, {"Ivan", ""}, {"  ", "Test"}} {
		u := incompleteUser()
		if err := u.CompleteSetup(names[0], names[1], "pwd", bothDocs, bothDocs, hasProviderStub(false), now); !errors.Is(err, userModel.ErrUserNameRequired.Err()) {
			t.Fatalf("names %q: want ErrUserNameRequired, got %v", names, err)
		}
		if u.Status != userModel.UserStatusIncomplete {
			t.Fatalf("must stay incomplete: %+v", u)
		}
	}
}

func TestCompleteSetup_RequiredAgainstAccepted(t *testing.T) {
	terms, privacy := []string{"terms"}, []string{"privacy"}
	for name, tc := range map[string]struct {
		required, accepted []string
		ok                 bool
		stored             []string
	}{
		"nothing required, nothing accepted":      {nil, nil, true, nil},
		"nothing required, something shown":       {nil, terms, true, nil},
		"terms required, nothing accepted":        {terms, nil, false, nil},
		"terms required, terms accepted":          {terms, terms, true, terms},
		"terms required, privacy accepted":        {terms, privacy, false, nil},
		"privacy required, privacy accepted":      {privacy, privacy, true, privacy},
		"privacy required, nothing accepted":      {privacy, []string{}, false, nil},
		"both required, one accepted":             {bothDocs, terms, false, nil},
		"both required, both accepted":            {bothDocs, bothDocs, true, bothDocs},
		"both required, reversed order":           {bothDocs, []string{"privacy", "terms"}, true, bothDocs},
		"both required, duplicates":               {bothDocs, []string{"terms", "terms", "privacy"}, true, bothDocs},
		"terms required, superset accepted":       {terms, bothDocs, true, bothDocs},
		"terms required, unknown name is ignored": {terms, []string{"terms", "other"}, true, terms},
		"terms required, only unknown":            {terms, []string{"other"}, false, nil},
	} {
		u := incompleteUser()
		err := u.CompleteSetup("a", "b", "pwd", tc.accepted, tc.required, hasProviderStub(false), now)
		if tc.ok != (err == nil) {
			t.Errorf("%s: err=%v want ok=%v", name, err, tc.ok)
			continue
		}
		if !tc.ok {
			if !errors.Is(err, authModel.ErrTosNotAccepted.Err()) {
				t.Errorf("%s: want ErrTosNotAccepted, got %v", name, err)
			}
			if u.Status != userModel.UserStatusIncomplete || u.TosAcceptedAt != nil || len(u.LegalDocuments) != 0 {
				t.Errorf("%s: a rejected setup must change nothing: %+v", name, u)
			}
			continue
		}
		if len(tc.required) == 0 {
			if u.TosAcceptedAt != nil || len(u.LegalDocuments) != 0 {
				t.Errorf("%s: nothing required means nothing stored: %+v", name, u)
			}
			continue
		}
		if u.TosAcceptedAt == nil || !u.TosAcceptedAt.Equal(now) || !reflect.DeepEqual(u.LegalDocuments, tc.stored) {
			t.Errorf("%s: stored %v at %v, want %v", name, u.LegalDocuments, u.TosAcceptedAt, tc.stored)
		}
	}
}
