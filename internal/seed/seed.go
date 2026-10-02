// Package seed fills a development database with test data for live end-to-end checks: verified
// accounts, a catalog of published exercises with lab topologies, and one event with registration
// open, teams, and every exercise attached. It drives the application's own use cases and
// repositories, is idempotent (a re-run updates or skips what exists, by stable names) and removes
// only what carries its marker (see catalog.go).
package seed

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/secret"
)

// Profile is the size of the seeded event.
type Profile struct {
	Name     string
	Teams    int
	TeamSize int
}

var profiles = []Profile{
	{Name: "small", Teams: 3, TeamSize: 3},
	{Name: "load", Teams: 16, TeamSize: 5},
}

// ProfileByName returns the named profile.
func ProfileByName(name string) (Profile, error) {
	for _, profile := range profiles {
		if profile.Name == name {
			return profile, nil
		}
	}
	names := make([]string, 0, len(profiles))
	for _, profile := range profiles {
		names = append(names, profile.Name)
	}
	return Profile{}, fmt.Errorf("unknown profile %q (one of: %s)", name, strings.Join(names, ", "))
}

// organizerCount: the event owner and one manager.
const organizerCount = 2

// Options configure one run.
type Options struct {
	Profile Profile
	// StartIn is how long from now the event starts; Duration is how long it lasts.
	StartIn  time.Duration
	Duration time.Duration
	// RevealMode is how infrastructure tasks are revealed to the teams.
	RevealMode eventConfigModel.TaskRevealMode
	// EventTag is the event subdomain; it must start with TagPrefix.
	EventTag string
	// Infrastructure allows dynamic labs on the event (and attaches the lab exercises).
	Infrastructure  bool
	CredentialsPath string
	// DryRun only lists what Cleanup would remove.
	DryRun bool
	// Out receives the progress lines; never a password.
	Out io.Writer
}

func (o Options) validate() error {
	if !strings.HasPrefix(o.EventTag, TagPrefix) {
		return fmt.Errorf("the event tag must start with %q, so cleanup can tell the seeded event", TagPrefix)
	}
	if o.StartIn < time.Minute {
		return fmt.Errorf("--start-in must be at least 1m: teams join while registration is open, and it closes at the start")
	}
	if o.Duration <= 0 {
		return fmt.Errorf("--duration must be positive")
	}
	if !o.RevealMode.Valid() {
		return fmt.Errorf("unknown task reveal mode %q (all_ready or as_ready)", o.RevealMode)
	}
	return nil
}

// Deps are what a seeder needs from the process: the database and the password policy.
type Deps struct {
	Queries  *postgres.Queries
	Pool     *pgxpool.Pool
	Password *password.Client
	// Cipher encrypts exercise secret env vars; the seeded exercises have none, so nil is fine.
	Cipher *secret.Cipher
}

// Seeder is the test-data generator.
type Seeder struct {
	deps         Deps
	users        *userRepo.Repository
	events       *eventRepo.Repository
	teams        *eventTeamRepo.Repository
	participants *participantRepo.Repository
	exercises    *exerciseUseCase.ExerciseUseCase
	eventUC      *eventUseCase.EventUseCase
	out          io.Writer
}

// labsAssumed stands in for a connected laboratory: the seeder writes data only, so an event may
// allow infrastructure while no agent is up. The runtime checks the real agent when labs deploy.
type labsAssumed struct{}

func (labsAssumed) RequireLaboratories(context.Context) error { return nil }

// noMedia: seeded exercises carry no attachments, so there is no file reference to keep.
type noMedia struct{}

func (noMedia) ReplaceReferences(context.Context, string, uuid.UUID, []uuid.UUID) error { return nil }
func (noMedia) RemoveReferences(context.Context, string, uuid.UUID) error               { return nil }
func (noMedia) RemoveReferencesBatch(context.Context, string, []uuid.UUID) error        { return nil }

// New wires the use cases the seeder drives. No signal publisher is wired, so seeding sends no
// notification mail.
func New(deps Deps) *Seeder {
	exercises := exerciseUseCase.NewExerciseUseCase(exerciseUseCase.Dependencies{Repo: deps.Queries, Cipher: deps.Cipher, Media: noMedia{}})
	events := eventUseCase.NewEventUseCase(eventUseCase.Dependencies{
		Repo:                     deps.Queries,
		UoW:                      postgres.NewUnitOfWorker[eventUseCase.IRepository](postgres.NewUoWFactory(deps.Pool)),
		InfrastructureCapability: labsAssumed{},
		Topologies:               exercises,
	})
	return &Seeder{
		deps:         deps,
		users:        userRepo.New(deps.Queries),
		events:       eventRepo.New(deps.Queries),
		teams:        eventTeamRepo.New(deps.Queries),
		participants: participantRepo.New(deps.Queries),
		exercises:    exercises,
		eventUC:      events,
		out:          io.Discard,
	}
}

func (s *Seeder) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.out, format+"\n", args...)
}

// Report is what a run produced; it holds no secret.
type Report struct {
	Users, UsersCreated         int
	Exercises, ExercisesChanged int
	EventID                     uuid.UUID
	EventTag                    string
	EventCreated                bool
	StartAt                     time.Time
	Teams, TeamsCreated         int
	CredentialsPath             string
}

// Seed brings the database to the seeded state.
func (s *Seeder) Seed(ctx context.Context, opts Options) (Report, error) {
	var report Report
	if err := opts.validate(); err != nil {
		return report, err
	}
	if opts.Out != nil {
		s.out = opts.Out
	}
	if opts.CredentialsPath == "" {
		opts.CredentialsPath = DefaultCredentialsPath
	}

	plain := readPassword(opts.CredentialsPath)
	if plain == "" || s.deps.Password.CheckPasswordComplexity(plain) != nil {
		var err error
		if plain, err = generatePassword(s.deps.Password.Complexity()); err != nil {
			return report, fmt.Errorf("generate password: %w", err)
		}
	}
	hash, err := s.deps.Password.Hash(plain)
	if err != nil {
		return report, fmt.Errorf("hash password: %w", err)
	}

	// Accounts: organizers first (the first owns the event), then the team members.
	var lines []credentialLine
	organizers := make([]userModel.User, 0, organizerCount)
	for i := 1; i <= organizerCount; i++ {
		user, created, ensureErr := s.ensureUser(ctx, userSpec{Email: organizerEmail(i), FirstName: "Organizer", LastName: fmt.Sprintf("No%d", i), Role: rbac.RoleAdmin}, plain, hash)
		if ensureErr != nil {
			return report, ensureErr
		}
		organizers = append(organizers, user)
		lines = append(lines, credentialLine{Email: user.Email, Role: "organizer (platform admin)"})
		report.Users++
		if created {
			report.UsersCreated++
		}
	}
	members := make([][]userModel.User, opts.Profile.Teams)
	for team := 1; team <= opts.Profile.Teams; team++ {
		for member := 1; member <= opts.Profile.TeamSize; member++ {
			user, created, ensureErr := s.ensureUser(ctx, userSpec{
				Email: participantEmail(team, member), FirstName: "Tester", LastName: fmt.Sprintf("T%02dM%d", team, member), Role: rbac.RoleUser,
			}, plain, hash)
			if ensureErr != nil {
				return report, ensureErr
			}
			members[team-1] = append(members[team-1], user)
			role := "participant"
			if member == 1 {
				role = "participant (captain)"
			}
			lines = append(lines, credentialLine{Email: user.Email, Role: role, Team: teamName(team)})
			report.Users++
			if created {
				report.UsersCreated++
			}
		}
	}
	header := fmt.Sprintf("profile: %s, event: %s, written: %s", opts.Profile.Name, opts.EventTag, time.Now().Format(time.RFC3339))
	if err = writeCredentials(opts.CredentialsPath, header, plain, lines); err != nil {
		return report, err
	}
	report.CredentialsPath = opts.CredentialsPath
	s.logf("accounts: %d (%d new), credentials in %s", report.Users, report.UsersCreated, opts.CredentialsPath)

	// Catalog.
	var ready []publishedExercise
	for _, spec := range catalog() {
		if hasLab(spec) && !opts.Infrastructure {
			continue
		}
		item, action, ensureErr := s.ensureExercise(ctx, spec, organizers[0].ID)
		if ensureErr != nil {
			return report, ensureErr
		}
		ready = append(ready, item)
		report.Exercises++
		if action != "kept" {
			report.ExercisesChanged++
		}
		s.logf("exercise %s: %s", spec.Name, action)
	}

	return s.seedEvent(ctx, opts, report, organizers, members, ready)
}

func hasLab(spec exerciseSpec) bool {
	for _, variant := range spec.Variants {
		if len(variant.Devices) > 0 {
			return true
		}
	}
	return false
}

func teamName(index int) string { return fmt.Sprintf("Seed Team %02d", index) }
