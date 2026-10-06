package seed

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/session"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/sessionRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// CleanupReport lists what was removed (or, on a dry run, what would be).
type CleanupReport struct {
	Events    []string
	Exercises []string
	Users     []string
	// Kept names what carries the marker but could not go, with the reason.
	Kept []string
}

// Cleanup removes everything the seeder created, and nothing else: the events whose tag starts
// with TagPrefix and whose name with NamePrefix, the catalog exercises tagged MarkerTag and named
// with NamePrefix, and the accounts on MailDomain. Order matters: an exercise cannot go while an
// event uses it, so events go first and accounts last. Users are deleted the way the platform
// deletes an account (soft delete: the address is freed, sessions are revoked).
func (s *Seeder) Cleanup(ctx context.Context, opts Options) (CleanupReport, error) {
	var report CleanupReport
	if opts.Out != nil {
		s.out = opts.Out
	}
	if opts.CredentialsPath == "" {
		opts.CredentialsPath = DefaultCredentialsPath
	}
	verb := "removed"
	if opts.DryRun {
		verb = "would remove"
	}

	// Events.
	cursorAt, cursorID := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC), uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
	for {
		page, err := s.events.ListCursor(ctx, eventRepo.ListParams{Search: TagPrefix, CursorCreatedAt: cursorAt, CursorID: cursorID, Limit: 100})
		if err != nil {
			return report, fmt.Errorf("list events: %w", err)
		}
		for _, event := range page {
			if !strings.HasPrefix(event.Tag, TagPrefix) || !strings.HasPrefix(event.InternalName, NamePrefix) {
				continue
			}
			if !opts.DryRun {
				if err = s.eventUC.DeleteEvent(ctx, event.ID); err != nil {
					return report, fmt.Errorf("delete event %s: %w", event.Tag, err)
				}
			}
			report.Events = append(report.Events, event.Tag)
			s.logf("event %s: %s", event.Tag, verb)
		}
		if len(page) < 100 {
			break
		}
		last := page[len(page)-1]
		cursorAt, cursorID = last.CreatedAt, last.ID
	}

	// Exercises, active and archived (an event deletion archives the ones it owned).
	for _, archived := range []string{"exclude", "only"} {
		var cursor uuid.UUID
		for {
			page, err := s.exercises.ListExercises(ctx, exerciseUseCase.ExercisesFilter{Tags: []string{MarkerTag}, Archived: archived, Cursor: cursor, PageSize: 50})
			if err != nil {
				return report, fmt.Errorf("list exercises: %w", err)
			}
			for _, item := range page.Exercises {
				if !strings.HasPrefix(item.Name, NamePrefix) || item.Scope != "catalog" {
					continue
				}
				if !opts.DryRun {
					if err = s.exercises.DeleteExercise(ctx, item.ID); err != nil {
						if errors.Is(err, exerciseModel.ErrExerciseInUse.Err()) {
							report.Kept = append(report.Kept, item.Name+": used by an event that is not seeded")
							s.logf("exercise %s: kept, used by an event that is not seeded", item.Name)
							continue
						}
						return report, fmt.Errorf("delete exercise %q: %w", item.Name, err)
					}
				}
				report.Exercises = append(report.Exercises, item.Name)
				s.logf("exercise %s: %s", item.Name, verb)
			}
			if !page.HasMore {
				break
			}
			cursor = page.NextCursor
		}
	}

	// Accounts.
	users, err := s.seededUsers(ctx)
	if err != nil {
		return report, err
	}
	sessions := sessionRepo.New(s.deps.Queries)
	for _, user := range users {
		if user.Role == rbac.RoleSuperAdmin {
			report.Kept = append(report.Kept, user.Email+": a super admin is never removed")
			continue
		}
		email := user.Email
		if !opts.DryRun {
			expected := user.UpdatedAt
			if err = user.SoftDelete(time.Now()); err != nil {
				return report, fmt.Errorf("delete user %s: %w", user.Email, err)
			}
			if affected, updateErr := s.users.Update(ctx, user, expected); updateErr != nil || affected == 0 {
				return report, fmt.Errorf("delete user %s: affected=%d err=%v", user.Email, affected, updateErr)
			}
			// The running replicas learn of it through the revocation list; the lifetimes are the generous ones (a
			// longer row is harmless, a shorter one would let a cookie outlive its revocation).
			if _, err = sessions.RevokeAllForUser(ctx, user.ID, seedSessionLifetimes); err != nil {
				return report, fmt.Errorf("revoke sessions of %s: %w", user.Email, err)
			}
			if _, err = s.users.DeleteProviders(ctx, user.ID); err != nil {
				return report, fmt.Errorf("remove providers of %s: %w", user.Email, err)
			}
		}
		report.Users = append(report.Users, email)
	}
	s.logf("accounts: %d %s", len(report.Users), verb)

	if !opts.DryRun {
		if err = removeCredentials(opts.CredentialsPath); err != nil {
			return report, err
		}
	}
	return report, nil
}

// seedSessionLifetimes are longer than any configured session lifetime.
var seedSessionLifetimes = session.Lifetimes{Idle: 30 * 24 * time.Hour, Absolute: 30 * 24 * time.Hour}
