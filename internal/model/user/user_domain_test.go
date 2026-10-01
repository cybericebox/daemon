package userModel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

var now = time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)

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

	err := u.CompleteSetup("Ivan", "Test", "hashed-pwd", 2, hasProviderStub(false), now)
	if err != nil {
		t.Fatalf("CompleteSetup: %v", err)
	}
	if u.Status != userModel.UserStatusActive || !u.EmailConfirmed {
		t.Fatalf("must become active+confirmed: %+v", u)
	}
	if u.FirstName != "Ivan" || u.LastName != "Test" || !u.HasPassword() {
		t.Fatalf("profile/password not applied: %+v", u)
	}
	if u.TosVersion != 2 || u.TosAcceptedAt == nil || !u.TosAcceptedAt.Equal(now) {
		t.Fatalf("ToS not recorded: %+v", u)
	}
	if !u.UpdatedAt.Equal(now) {
		t.Fatalf("UpdatedAt not touched: %v", u.UpdatedAt)
	}
}

func TestCompleteSetup_Invariants(t *testing.T) {
	active := incompleteUser()
	_ = active.CompleteSetup("a", "b", "pwd", 1, hasProviderStub(false), now)
	if err := active.CompleteSetup("x", "y", "pwd", 1, hasProviderStub(false), now); !errors.Is(err, authModel.ErrSetupAlreadyComplete.Err()) {
		t.Fatalf("second setup: want ErrSetupAlreadyComplete, got %v", err)
	}

	u := incompleteUser()
	if err := u.CompleteSetup("a", "b", "pwd", 0, hasProviderStub(false), now); !errors.Is(err, authModel.ErrTosNotAccepted.Err()) {
		t.Fatalf("tosVersion 0: want ErrTosNotAccepted, got %v", err)
	}

	u = incompleteUser()
	if err := u.CompleteSetup("a", "b", "", 1, hasProviderStub(false), now); !errors.Is(err, authModel.ErrNoLoginMethod.Err()) {
		t.Fatalf("no password, no provider: want ErrNoLoginMethod, got %v", err)
	}
	// provider-linked account may finish without a password
	u = incompleteUser()
	if err := u.CompleteSetup("a", "b", "", 1, hasProviderStub(true), now); err != nil {
		t.Fatalf("provider-only setup must pass: %v", err)
	}
	if u.HasPassword() {
		t.Fatal("no password expected")
	}
}

func TestBlockActivate_Transitions(t *testing.T) {
	u := incompleteUser()
	_ = u.CompleteSetup("a", "b", "pwd", 1, hasProviderStub(false), now)

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
	_ = del.CompleteSetup("a", "b", "pwd", 1, hasProviderStub(false), now)
	_ = del.SoftDelete(now)
	if err := del.Activate(now); !errors.Is(err, authModel.ErrInvalidUserStatus.Err()) {
		t.Fatalf("deleted cannot be activated: got %v", err)
	}
}

func TestSoftDelete_ScrubsPIIAndIsNotRepeatable(t *testing.T) {
	u := incompleteUser()
	_ = u.CompleteSetup("Ivan", "Test", "pwd", 1, hasProviderStub(false), now)
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
	_ = u.CompleteSetup("a", "b", "pwd", 1, hasProviderStub(false), now)

	if err := u.ChangeRole(rbac.RoleAdmin, now); err != nil || u.Role != rbac.RoleAdmin {
		t.Fatalf("valid role must apply: %v %s", err, u.Role)
	}
	if err := u.ChangeRole("ghost", now); !errors.Is(err, authModel.ErrInvalidRole.Err()) {
		t.Fatalf("invalid role: want ErrInvalidRole, got %v", err)
	}
}

func TestSmallMutators_TouchUpdatedAt(t *testing.T) {
	u := incompleteUser()
	_ = u.CompleteSetup("a", "b", "pwd", 1, hasProviderStub(false), now.Add(-time.Minute))

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
	_ = u.CompleteSetup("a", "b", "pwd", 1, hasProviderStub(false), now)
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
