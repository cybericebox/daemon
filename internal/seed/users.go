package seed

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// seedTosVersion is the terms version the seeded accounts accepted.
const seedTosVersion = 1

type userSpec struct {
	Email     string
	FirstName string
	LastName  string
	Role      rbac.Role
	Team      string
}

func participantEmail(team, member int) string {
	return fmt.Sprintf("seed-t%02d-m%d@%s", team, member, MailDomain)
}

func organizerEmail(index int) string {
	return fmt.Sprintf("seed-org%d@%s", index, MailDomain)
}

// ensureUser creates the account (verified, active, with the password hash) or brings an
// existing one in line: active, confirmed, the wanted role and password. Returns whether it was
// created.
func (s *Seeder) ensureUser(ctx context.Context, spec userSpec, plain, hash string) (userModel.User, bool, error) {
	now := time.Now()
	existing, err := s.users.GetByEmail(ctx, spec.Email)
	if err != nil {
		if !repositoryTools.IsObjectNotFoundError(err) {
			return userModel.User{}, false, fmt.Errorf("get user %s: %w", spec.Email, err)
		}
		created, createErr := s.users.Create(ctx, userModel.NewInvitedUser(uuid.Must(uuid.NewV7()), spec.Email, spec.FirstName, spec.LastName, spec.Role, now))
		if createErr != nil {
			return userModel.User{}, false, fmt.Errorf("create user %s: %w", spec.Email, createErr)
		}
		expected := created.UpdatedAt
		if err = created.CompleteSetup(spec.FirstName, spec.LastName, hash, seedTosVersion, func() (bool, error) { return false, nil }, now); err != nil {
			return userModel.User{}, false, fmt.Errorf("complete user %s: %w", spec.Email, err)
		}
		if affected, updateErr := s.users.Update(ctx, created, expected); updateErr != nil || affected == 0 {
			return userModel.User{}, false, fmt.Errorf("activate user %s: affected=%d err=%v", spec.Email, affected, updateErr)
		}
		return created, true, nil
	}

	expected := existing.UpdatedAt
	changed := false
	if existing.Status == userModel.UserStatusBlocked {
		if err = existing.Activate(now); err != nil {
			return userModel.User{}, false, err
		}
		changed = true
	}
	if existing.Status == userModel.UserStatusIncomplete {
		if err = existing.CompleteSetup(spec.FirstName, spec.LastName, hash, seedTosVersion, func() (bool, error) { return false, nil }, now); err != nil {
			return userModel.User{}, false, err
		}
		changed = true
	}
	if existing.Role != spec.Role {
		if err = existing.ChangeRole(spec.Role, now); err != nil {
			return userModel.User{}, false, err
		}
		changed = true
	}
	if !existing.EmailConfirmed {
		existing.ConfirmEmail(now)
		changed = true
	}
	if ok, _ := s.deps.Password.Matches(plain, existing.HashedPassword); !ok {
		existing.SetPassword(hash, now)
		changed = true
	}
	if changed {
		if affected, updateErr := s.users.Update(ctx, existing, expected); updateErr != nil || affected == 0 {
			return userModel.User{}, false, fmt.Errorf("update user %s: affected=%d err=%v", spec.Email, affected, updateErr)
		}
	}
	return existing, false, nil
}

// seededUsers lists every live account on the seed mail domain.
func (s *Seeder) seededUsers(ctx context.Context) ([]userModel.User, error) {
	var out []userModel.User
	cursorAt, cursorID := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
	for {
		page, err := s.users.ListCursor(ctx, userRepo.ListParams{Search: "@" + MailDomain, Roles: []string{}, CursorCreatedAt: cursorAt, CursorID: cursorID, Limit: 200})
		if err != nil {
			return nil, fmt.Errorf("list seeded users: %w", err)
		}
		for _, user := range page {
			if strings.HasSuffix(strings.ToLower(user.Email), "@"+MailDomain) {
				out = append(out, user)
			}
		}
		if len(page) < 200 {
			return out, nil
		}
		last := page[len(page)-1]
		cursorAt, cursorID = last.CreatedAt, last.ID
	}
}
