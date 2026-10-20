// Package loadtest creates and removes the accounts of a load-test run. The accounts are real,
// confirmed users with a random password and one open session each, all carrying a run marker in
// the e-mail address, so one command removes exactly them. No captcha or security setting is
// touched: the service writes the rows and seals the session cookie the way a sign-in would.
package loadtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/sessionRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/seed"
	sessionPkg "github.com/cybericebox/daemon/internal/session"
	"github.com/cybericebox/daemon/pkg/password"
)

const (
	// MailDomain ends every load-test address. It is a reserved name that can never be delivered.
	MailDomain = "loadtest.invalid"
	// Prefix starts every local part: lt-<run>-<NNN>.
	Prefix = "lt-"
	// MaxUsers caps one run, a guard against a typo in --count.
	MaxUsers = 2000

	tosVersion = 1
	pageSize   = 200
)

var runPattern = regexp.MustCompile(`^[a-z0-9]{3,20}$`)

// ValidateRun checks a run id: lower-case letters and digits only, so it never clashes with the
// «-» separators of the address.
func ValidateRun(run string) error {
	if !runPattern.MatchString(run) {
		return fmt.Errorf("run id %q must be 3-20 characters of a-z and 0-9", run)
	}
	return nil
}

// Email is the address of user number n (1-based) of a run.
func Email(run string, n int) string {
	return fmt.Sprintf("%s%s-%03d@%s", Prefix, run, n, MailDomain)
}

// RunSuffix and RunPrefix delimit the addresses of one run.
func runPrefix(run string) string { return Prefix + run + "-" }

// BelongsToRun reports whether an address is a load-test address of this run.
func BelongsToRun(email, run string) bool {
	e := strings.ToLower(email)
	return strings.HasPrefix(e, runPrefix(run)) && strings.HasSuffix(e, "@"+MailDomain)
}

// Account is one line of the credentials file.
type Account struct {
	N        int       `json:"n"`
	Email    string    `json:"email"`
	Password string    `json:"password"`
	UserID   uuid.UUID `json:"userId"`
	// Manual marks an account kept for a person: the load tool never touches it. It has no session; the owner
	// signs in with the password and goes through the event registration in the browser.
	Manual bool `json:"manual,omitempty"`
	// Cookie is the sealed session cookie value (name Cookie name in the file header). It lets a
	// load tool skip the captcha-protected sign-in.
	Cookie string `json:"cookie"`
}

// File is the credentials file: written once, read by the load tool, never committed.
type File struct {
	Run        string    `json:"run"`
	CreatedAt  time.Time `json:"createdAt"`
	CookieName string    `json:"cookieName"`
	// CookieExpiresAt is when the cookies stop opening (the idle TTL of the platform).
	CookieExpiresAt time.Time `json:"cookieExpiresAt"`
	Accounts        []Account `json:"accounts"`
}

// Users is the narrow user repository the service needs.
type Users interface {
	GetByEmail(ctx context.Context, email string) (userModel.User, error)
	Create(ctx context.Context, u userModel.User) (userModel.User, error)
	Update(ctx context.Context, u userModel.User, expectedUpdatedAt time.Time) (int64, error)
	ListCursor(ctx context.Context, p userRepo.ListParams) ([]userModel.User, error)
	DeleteProviders(ctx context.Context, userID uuid.UUID) (int64, error)
}

// Sessions is the narrow session repository the service needs.
type Sessions interface {
	Create(ctx context.Context, s authModel.Session) (authModel.Session, error)
	RevokeAllForUser(ctx context.Context, userID uuid.UUID, l sessionPkg.Lifetimes) ([]sessionRepo.Revoked, error)
}

// Config is what the service needs from the platform configuration.
type Config struct {
	Lifetimes  sessionPkg.Lifetimes
	Complexity password.ComplexityConfig
}

// Service creates and removes load-test accounts.
type Service struct {
	users    Users
	sessions Sessions
	codec    *sessionPkg.Codec
	hasher   func(string) (string, error)
	cfg      Config
	now      func() time.Time
	out      io.Writer
}

// New builds the service; out receives progress lines (never a secret).
func New(users Users, sessions Sessions, codec *sessionPkg.Codec, hasher func(string) (string, error), cfg Config, out io.Writer) *Service {
	if out == nil {
		out = io.Discard
	}
	return &Service{users: users, sessions: sessions, codec: codec, hasher: hasher, cfg: cfg, now: time.Now, out: out}
}

// WithClock replaces the clock (tests).
func (s *Service) WithClock(now func() time.Time) *Service { s.now = now; return s }

func (s *Service) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.out, format+"\n", args...)
}

// Create makes count confirmed users for the run, each with an open session, and returns the
// credentials. A run that already has accounts is refused: a new run id keeps cleanup exact.
func (s *Service) Create(ctx context.Context, run string, count, manual int) (File, error) {
	if err := ValidateRun(run); err != nil {
		return File{}, err
	}
	if count < 1 || manual < 0 || count+manual > MaxUsers {
		return File{}, fmt.Errorf("count must be at least 1 and count plus manual at most %d", MaxUsers)
	}
	if _, err := s.users.GetByEmail(ctx, Email(run, 1)); err == nil {
		return File{}, fmt.Errorf("run %q already has accounts; delete them or pick another run id", run)
	} else if !repositoryTools.IsObjectNotFoundError(err) {
		return File{}, fmt.Errorf("check run: %w", err)
	}

	now := s.now()
	file := File{Run: run, CreatedAt: now, CookieName: authModel.SessionCookie, CookieExpiresAt: now.Add(s.cfg.Lifetimes.Idle)}
	for n := 1; n <= count+manual; n++ {
		account, err := s.createOne(ctx, run, n, now, n > count)
		if err != nil {
			return file, fmt.Errorf("account %d of %d: %w", n, count+manual, err)
		}
		file.Accounts = append(file.Accounts, account)
	}
	s.logf("created %d accounts (%d manual) for run %s", count+manual, manual, run)
	return file, nil
}

func (s *Service) createOne(ctx context.Context, run string, n int, now time.Time, manual bool) (Account, error) {
	email := Email(run, n)
	plain, err := seed.GeneratePassword(s.cfg.Complexity)
	if err != nil {
		return Account{}, fmt.Errorf("generate password: %w", err)
	}
	hash, err := s.hasher(plain)
	if err != nil {
		return Account{}, fmt.Errorf("hash password: %w", err)
	}
	first, last := "Load", fmt.Sprintf("Test%03d", n)
	created, err := s.users.Create(ctx, userModel.NewInvitedUser(uuid.Must(uuid.NewV7()), email, first, last, rbac.RoleUser, now))
	if err != nil {
		return Account{}, fmt.Errorf("create user: %w", err)
	}
	expected := created.UpdatedAt
	if err = created.CompleteSetup(first, last, hash, tosVersion, func() (bool, error) { return false, nil }, now); err != nil {
		return Account{}, fmt.Errorf("complete setup: %w", err)
	}
	if affected, updateErr := s.users.Update(ctx, created, expected); updateErr != nil || affected == 0 {
		return Account{}, fmt.Errorf("activate user: affected=%d err=%v", affected, updateErr)
	}
	if manual {
		return Account{N: n, Email: email, Password: plain, UserID: created.ID, Manual: true}, nil
	}
	session, err := s.sessions.Create(ctx, authModel.NewSession(created.ID, authModel.SessionMetadata{UserAgent: "cybericebox-loadtest/" + run}, s.cfg.Lifetimes.Idle, now))
	if err != nil {
		return Account{}, fmt.Errorf("create session: %w", err)
	}
	cookie, err := s.codec.Seal(sessionPkg.NewTicket(session.ID, created.ID, now, s.cfg.Lifetimes))
	if err != nil {
		return Account{}, fmt.Errorf("seal cookie: %w", err)
	}
	return Account{N: n, Email: email, Password: plain, UserID: created.ID, Cookie: cookie}, nil
}

// DeleteReport says what a delete did.
type DeleteReport struct {
	Deleted int
	Kept    []string
}

// Delete removes every account of the run the way the platform deletes an account: soft delete
// (the address is freed, personal data cleared), sessions revoked, providers removed. Only the
// addresses that carry the run marker are touched; a privileged account is never removed. The
// rows of the load-test event (teams, attempts) go with the event itself.
func (s *Service) Delete(ctx context.Context, run string, dryRun bool) (DeleteReport, error) {
	var report DeleteReport
	if err := ValidateRun(run); err != nil {
		return report, err
	}
	accounts, err := s.listRun(ctx, run)
	if err != nil {
		return report, err
	}
	lifetimes := sessionPkg.Lifetimes{Idle: 30 * 24 * time.Hour, Absolute: 30 * 24 * time.Hour}
	for _, user := range accounts {
		if user.Role != rbac.RoleUser {
			report.Kept = append(report.Kept, fmt.Sprintf("%s: role %s is never removed", user.Email, user.Role))
			continue
		}
		if !dryRun {
			expected := user.UpdatedAt
			if err = user.SoftDelete(s.now()); err != nil {
				return report, fmt.Errorf("delete user %s: %w", user.Email, err)
			}
			if affected, updateErr := s.users.Update(ctx, user, expected); updateErr != nil || affected == 0 {
				return report, fmt.Errorf("delete user %s: affected=%d err=%v", user.Email, affected, updateErr)
			}
			if _, err = s.sessions.RevokeAllForUser(ctx, user.ID, lifetimes); err != nil {
				return report, fmt.Errorf("revoke sessions of %s: %w", user.Email, err)
			}
			if _, err = s.users.DeleteProviders(ctx, user.ID); err != nil {
				return report, fmt.Errorf("remove providers of %s: %w", user.Email, err)
			}
		}
		report.Deleted++
	}
	verb := "deleted"
	if dryRun {
		verb = "would delete"
	}
	s.logf("run %s: %s %d accounts, kept %d", run, verb, report.Deleted, len(report.Kept))
	return report, nil
}

// listRun pages through the live accounts whose address carries the run marker.
func (s *Service) listRun(ctx context.Context, run string) ([]userModel.User, error) {
	var out []userModel.User
	cursorAt := time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	cursorID := uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
	for {
		page, err := s.users.ListCursor(ctx, userRepo.ListParams{Search: runPrefix(run), Roles: []string{}, CursorCreatedAt: cursorAt, CursorID: cursorID, Limit: pageSize})
		if err != nil {
			return nil, fmt.Errorf("list run accounts: %w", err)
		}
		for _, user := range page {
			if BelongsToRun(user.Email, run) {
				out = append(out, user)
			}
		}
		if len(page) < pageSize {
			return out, nil
		}
		last := page[len(page)-1]
		cursorAt, cursorID = last.CreatedAt, last.ID
	}
}

// Write stores the credentials as JSON (mode 0600). A path of «-» writes to w (stdout), which is
// how a run inside the backend pod hands the file to the operator without putting secrets into
// a pod log or onto the pod disk.
func Write(path string, w io.Writer, file File) error {
	data, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if path == "-" {
		_, err = w.Write(data)
		return err
	}
	if path == "" {
		return errors.New("an output path is required (use - for stdout)")
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	return os.Chmod(path, 0o600)
}

// Read loads a credentials file.
func Read(path string) (File, error) {
	var file File
	data, err := os.ReadFile(path)
	if err != nil {
		return file, err
	}
	if err = json.Unmarshal(data, &file); err != nil {
		return file, fmt.Errorf("parse %s: %w", path, err)
	}
	return file, nil
}
