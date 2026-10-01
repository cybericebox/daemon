package userModel

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// User is the domain entity for a platform account. All mutations live on the
// entity (go-ddd): they enforce invariants and touch UpdatedAt in one place;
// now is always a parameter so callers and tests control the clock.
type User struct {
	ID             uuid.UUID
	Email          string
	FirstName      string
	LastName       string
	HashedPassword string
	Picture        string
	Role           rbac.Role
	Status         UserStatus
	EmailConfirmed bool
	TosAcceptedAt  *time.Time
	TosVersion     int32
	LastSeen       time.Time
	UpdatedAt      time.Time
	UpdatedBy      uuid.NullUUID
	DeletedAt      *time.Time
	CreatedAt      time.Time
}

// NotificationProfile is the subset of an account that may be passed to a
// notification payload filler. It intentionally excludes authentication and
// administrative state.
type NotificationProfile struct {
	ID        uuid.UUID
	Email     string
	FirstName string
	LastName  string
	Picture   string
}

func (u User) NotificationProfile() NotificationProfile {
	return NotificationProfile{
		ID:        u.ID,
		Email:     u.Email,
		FirstName: u.FirstName,
		LastName:  u.LastName,
		Picture:   u.Picture,
	}
}

// NewIncompleteUser is the factory for the email-first registration flow: the
// account exists (so the setup link can resolve it) but has no profile,
// password, or confirmation yet.
func NewIncompleteUser(id uuid.UUID, email string, now time.Time) User {
	return User{
		ID:        id,
		Email:     email,
		Role:      rbac.RoleUser,
		Status:    UserStatusIncomplete,
		LastSeen:  now,
		UpdatedAt: now,
		CreatedAt: now,
	}
}

// NewInvitedUser is the factory for admin invitations: the account carries the
// invited role and name and is email-preconfirmed (the invitation itself is
// admin-authorized), but stays incomplete until the invitee finishes setup.
func NewInvitedUser(id uuid.UUID, email, firstName, lastName string, role rbac.Role, now time.Time) User {
	u := NewIncompleteUser(id, email, now)
	u.FirstName = firstName
	u.LastName = lastName
	u.Role = role
	u.EmailConfirmed = true
	return u
}

// NewGoogleUser is the factory for Google-first registration: the provider
// vouches for the email, so it is confirmed from the start; the account stays
// incomplete until setup finishes.
func NewGoogleUser(id uuid.UUID, email, firstName, lastName string, now time.Time) User {
	u := NewIncompleteUser(id, email, now)
	u.FirstName = firstName
	u.LastName = lastName
	u.EmailConfirmed = true
	return u
}

// FullName returns the user's display name combined from first and last name.
func (u *User) FullName() string {
	return strings.TrimSpace(strings.TrimSpace(u.FirstName) + " " + strings.TrimSpace(u.LastName))
}

// HasPassword reports whether the account has a password login method.
func (u *User) HasPassword() bool {
	return u.HashedPassword != ""
}

func (u *User) IsIncomplete() bool { return u.Status == UserStatusIncomplete }
func (u *User) IsBlocked() bool    { return u.Status == UserStatusBlocked }
func (u *User) IsDeleted() bool    { return u.Status == UserStatusDeleted }

// SeenAfter reports whether the owner was active after t (an inactive-account
// warning at t is void once they sign in again).
func (u *User) SeenAfter(t time.Time) bool { return u.LastSeen.After(t) }

// touch moves UpdatedAt; every mutation goes through it.
func (u *User) touch(now time.Time) {
	u.UpdatedAt = now
}

// CompleteSetup finalizes an incomplete account: profile, optional password,
// ToS acceptance, email confirmation, and activation — enforcing every setup
// invariant in one place, in the mandated order (already-complete → ToS →
// login method). hasProvider is a lazy lookup ("is a social provider
// linked?") so the check — typically a DB read — runs only when no password
// is supplied.
func (u *User) CompleteSetup(firstName, lastName, hashedPassword string, tosVersion int32, hasProvider func() (bool, error), now time.Time) error {
	if !u.IsIncomplete() {
		return authModel.ErrSetupAlreadyComplete.Err()
	}
	if tosVersion <= 0 {
		return authModel.ErrTosNotAccepted.Err()
	}
	if hashedPassword == "" {
		linked, err := hasProvider()
		if err != nil {
			return err
		}
		if !linked {
			return authModel.ErrNoLoginMethod.Err()
		}
	}

	u.FirstName = firstName
	u.LastName = lastName
	if hashedPassword != "" {
		u.HashedPassword = hashedPassword
	}
	u.TosAcceptedAt = new(now)
	u.TosVersion = tosVersion
	u.EmailConfirmed = true
	u.Status = UserStatusActive
	u.touch(now)
	return nil
}

// UpdateProfile changes the display name.
func (u *User) UpdateProfile(firstName, lastName string, now time.Time) {
	u.FirstName = firstName
	u.LastName = lastName
	u.touch(now)
}

// ChangeEmail sets a new (already verified by the flow) email address.
func (u *User) ChangeEmail(email string, now time.Time) {
	u.Email = email
	u.touch(now)
}

// SetPassword replaces the password hash.
func (u *User) SetPassword(hashedPassword string, now time.Time) {
	u.HashedPassword = hashedPassword
	u.touch(now)
}

// SetPicture sets (or clears, with "") the avatar storage key.
func (u *User) SetPicture(picture string, now time.Time) {
	u.Picture = picture
	u.touch(now)
}

// ConfirmEmail marks the address as verified.
func (u *User) ConfirmEmail(now time.Time) {
	u.EmailConfirmed = true
	u.touch(now)
}

// Block suspends an account. Only the active↔blocked pair is a valid admin
// transition; incomplete and deleted accounts cannot be blocked.
func (u *User) Block(now time.Time) error {
	if u.Status != UserStatusActive && u.Status != UserStatusBlocked {
		return authModel.ErrInvalidUserStatus.Err()
	}
	u.Status = UserStatusBlocked
	u.touch(now)
	return nil
}

// Activate lifts a block. Incomplete and deleted accounts cannot be activated
// this way (incomplete activates only via CompleteSetup).
func (u *User) Activate(now time.Time) error {
	if u.Status != UserStatusActive && u.Status != UserStatusBlocked {
		return authModel.ErrInvalidUserStatus.Err()
	}
	u.Status = UserStatusActive
	u.touch(now)
	return nil
}

// SoftDelete marks the account deleted and scrubs PII (email is replaced with
// a tombstone so the unique constraint frees the address for re-registration).
// A second delete reports the user as already gone.
func (u *User) SoftDelete(now time.Time) error {
	if u.IsDeleted() {
		return ErrUserNotFound.Err()
	}
	u.DeletedAt = new(now)
	u.Status = UserStatusDeleted
	u.Email = "deleted+" + u.ID.String() + "@deleted.local"
	u.HashedPassword = ""
	u.FirstName = ""
	u.LastName = ""
	u.Picture = ""
	u.touch(now)
	return nil
}

// ChangeRole assigns a new role. Authorization (CanAssignRole, last-super-admin
// guard) is the use case's job — those need the caller's identity and DB counts;
// the entity only enforces that the role itself is assignable.
func (u *User) ChangeRole(role rbac.Role, now time.Time) error {
	if !rbac.ValidRole(string(role)) {
		return authModel.ErrInvalidRole.Err()
	}
	u.Role = role
	u.touch(now)
	return nil
}
