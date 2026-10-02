package seed_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/seed"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/pkg/password"
)

func newSeeder(t *testing.T) (*seed.Seeder, *testhelpers.TestDB) {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	policy := password.ComplexityConfig{MinLength: 8, MaxLength: 72, MinCapitalLetters: 1, MinSmallLetters: 1, MinDigits: 1}
	return seed.New(seed.Deps{Queries: db.Queries, Pool: db.Pool, Password: password.New(password.Config{HashCost: bcrypt.MinCost, Complexity: policy})}), db
}

func count(t *testing.T, db *testhelpers.TestDB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func options(t *testing.T, out *bytes.Buffer) seed.Options {
	t.Helper()
	profile, err := seed.ProfileByName("small")
	if err != nil {
		t.Fatal(err)
	}
	return seed.Options{
		Profile: profile, StartIn: 30 * time.Minute, Duration: 3 * time.Hour, RevealMode: eventConfigModel.RevealAsReady,
		EventTag: seed.DefaultEventTag, Infrastructure: true, CredentialsPath: filepath.Join(t.TempDir(), "creds"), Out: out,
	}
}

// TestSeedIsIdempotentAndCleanupRemovesOnlyTheSeeded drives the whole generator against a real
// database: the first run builds everything, the second changes nothing, and --delete removes
// what carries the marker while an unrelated account and exercise stay.
func TestSeedIsIdempotentAndCleanupRemovesOnlyTheSeeded(t *testing.T) {
	seeder, db := newSeeder(t)
	ctx := context.Background()
	var out bytes.Buffer
	opts := options(t, &out)

	// Something that is not ours and must survive.
	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, first_name, last_name, role, status, email_confirmed, last_seen, created_at)
		VALUES (gen_random_uuid(), 'real.person@example.com', 'Real', 'Person', 'user', 'active', true, now(), now())`); err != nil {
		t.Fatal(err)
	}

	first, err := seeder.Seed(ctx, opts)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if first.Users != 2+9 || first.UsersCreated != 11 || first.Exercises != 10 || first.Teams != 3 || first.TeamsCreated != 3 || !first.EventCreated {
		t.Fatalf("first report = %+v", first)
	}
	if n := count(t, db, `SELECT count(*) FROM event_teams WHERE event_id = $1`, first.EventID); n != 3 {
		t.Fatalf("teams = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM event_participants WHERE event_id = $1 AND status = 2 AND team_id IS NOT NULL`, first.EventID); n != 9 {
		t.Fatalf("team members = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM event_exercises WHERE event_id = $1`, first.EventID); n != 10 {
		t.Fatalf("attached exercises = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM event_challenges ec JOIN event_exercises ee ON ee.id = ec.event_exercise_id WHERE ee.event_id = $1 AND ec.published`, first.EventID); n != 20 {
		t.Fatalf("published challenges = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM event_challenge_prerequisites`); n != 4 {
		t.Fatalf("prerequisites = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM event_challenge_groups WHERE event_id = $1`, first.EventID); n != 4 {
		t.Fatalf("challenge groups = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM event_configs WHERE event_id = $1 AND task_reveal_mode = 'as_ready' AND registration = 2`, first.EventID); n != 1 {
		t.Fatal("config: reveal mode or open registration not set")
	}
	if n := count(t, db, `SELECT count(*) FROM events WHERE id = $1 AND infrastructure_allowed`, first.EventID); n != 1 {
		t.Fatal("infrastructure not allowed on the event")
	}

	// The credentials file holds the password; nothing else does.
	data, err := os.ReadFile(opts.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(opts.CredentialsPath)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials mode = %v", info.Mode().Perm())
	}
	var plain string
	for _, line := range strings.Split(string(data), "\n") {
		if value, ok := strings.CutPrefix(line, "password: "); ok {
			plain = value
		}
	}
	if plain == "" || strings.Contains(out.String(), plain) {
		t.Fatalf("password missing from the file or leaked to the output (%q)", plain)
	}

	// Second run: nothing new, the password is kept, the start moves.
	out.Reset()
	second, err := seeder.Seed(ctx, opts)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if second.UsersCreated != 0 || second.ExercisesChanged != 0 || second.TeamsCreated != 0 || second.EventCreated || second.EventID != first.EventID {
		t.Fatalf("second report = %+v", second)
	}
	if n := count(t, db, `SELECT count(*) FROM event_exercises WHERE event_id = $1`, first.EventID); n != 10 {
		t.Fatalf("attached exercises after the re-run = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM exercise_versions`); n != 10 {
		t.Fatalf("versions after the re-run = %d (an unchanged definition must not be re-published)", n)
	}
	again, _ := os.ReadFile(opts.CredentialsPath)
	if !strings.Contains(string(again), "password: "+plain) {
		t.Fatal("the password changed on a re-run")
	}

	// Dry run lists, removes nothing.
	dry := opts
	dry.DryRun = true
	preview, err := seeder.Cleanup(ctx, dry)
	if err != nil || len(preview.Events) != 1 || len(preview.Exercises) != 10 || len(preview.Users) != 11 {
		t.Fatalf("dry run = %+v, %v", preview, err)
	}
	if n := count(t, db, `SELECT count(*) FROM events`); n != 1 {
		t.Fatal("a dry run removed the event")
	}

	removed, err := seeder.Cleanup(ctx, opts)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if len(removed.Events) != 1 || len(removed.Exercises) != 10 || len(removed.Users) != 11 || len(removed.Kept) != 0 {
		t.Fatalf("cleanup report = %+v", removed)
	}
	if n := count(t, db, `SELECT count(*) FROM events`); n != 0 {
		t.Fatalf("events left = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM exercises`); n != 0 {
		t.Fatalf("exercises left = %d", n)
	}
	if n := count(t, db, `SELECT count(*) FROM users WHERE deleted_at IS NULL`); n != 1 {
		t.Fatalf("live users left = %d, want only the unrelated one", n)
	}
	if n := count(t, db, `SELECT count(*) FROM users WHERE email = 'real.person@example.com' AND deleted_at IS NULL`); n != 1 {
		t.Fatal("cleanup touched an account that is not seeded")
	}
	if _, err = os.Stat(opts.CredentialsPath); !os.IsNotExist(err) {
		t.Fatal("the credentials file was not removed")
	}

	// And it can be seeded again from scratch.
	if _, err = seeder.Seed(ctx, opts); err != nil {
		t.Fatalf("seed after cleanup: %v", err)
	}
}

func TestSeedRefusesAnEventTagOfSomeoneElse(t *testing.T) {
	seeder, db := newSeeder(t)
	ctx := context.Background()
	var out bytes.Buffer
	opts := options(t, &out)
	opts.EventTag = "other"
	if _, err := seeder.Seed(ctx, opts); err == nil || !strings.Contains(err.Error(), "must start with") {
		t.Fatalf("err = %v", err)
	}
	if n := count(t, db, `SELECT count(*) FROM users`); n != 0 {
		t.Fatal("a refused run wrote accounts")
	}
}
