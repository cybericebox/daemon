package keyrotation_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/keyRotationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/secretKeysRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
	"github.com/cybericebox/daemon/internal/useCase/secretkeys"
	"github.com/cybericebox/daemon/pkg/secret"
)

var (
	hexOld = strings.Repeat("11", 32)
	hexNew = strings.Repeat("22", 32)
)

// Fixed ids so the order of rows (the job walks by id) is known.
func id(n int) uuid.UUID {
	return uuid.FromStringOrNil(fmt.Sprintf("00000000-0000-4000-8000-%012d", n))
}

type world struct {
	t     *testing.T
	pool  *pgxpool.Pool
	repo  *keyRotationRepo.Repository
	oldC  *secret.Cipher // k1 only: what sealed the old rows
	ring  *secret.Cipher // k2 current, k1 still opens
	ctxs  keyRotationRepo.Contexts
	exec  func(sql string, args ...any)
	clock time.Time
}

func newWorld(t *testing.T) *world {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	conn, err := db.Pool.Acquire(ctx)
	require.NoError(t, err)
	t.Cleanup(conn.Release)
	_, err = conn.Exec(ctx, `SET session_replication_role = replica`) // the rows reference users and events; not under test
	require.NoError(t, err)
	// All statements of the fixture and the job must see the same session setting, so the job uses a pool that does
	// not need it: the foreign keys are not checked by the job's UPDATEs.
	w := &world{t: t, pool: db.Pool, ctxs: keyrotation.Contexts(), clock: time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)}
	w.repo = keyRotationRepo.New(db.Pool, w.ctxs)
	w.oldC, err = secret.New("k1:" + hexOld)
	require.NoError(t, err)
	w.ring, err = secret.New("k2:" + hexNew + ",k1:" + hexOld)
	require.NoError(t, err)
	w.exec = func(sql string, args ...any) {
		t.Helper()
		_, execErr := conn.Exec(ctx, sql, args...)
		require.NoError(t, execErr, sql)
	}
	return w
}

func (w *world) seal(c *secret.Cipher, plain string, ctx []byte) string {
	w.t.Helper()
	ct, err := c.EncryptWithContext([]byte(plain), ctx)
	require.NoError(w.t, err)
	return ct
}

func (w *world) smtp(n int, ct string) {
	w.exec(`INSERT INTO mail_smtp_configs (id, host, port, tls_mode, password_ciphertext, name) VALUES ($1, 'smtp.example', 587, 'starttls', $2, $3)`, id(n), ct, fmt.Sprintf("smtp-%d", n))
}

func (w *world) smtpValue(n int) string {
	var ct string
	require.NoError(w.t, w.pool.QueryRow(context.Background(), `SELECT password_ciphertext FROM mail_smtp_configs WHERE id = $1`, id(n)).Scan(&ct))
	return ct
}

func (w *world) agent(n int, client, access string) {
	w.exec(`INSERT INTO infrastructure_agents (id, key, name, configured, created_at, updated_at, client_key_ciphertext, access_private_key_ciphertext)
VALUES ($1, $2, $2, true, $3, $3, $4, $5)`, id(n), fmt.Sprintf("agent-%d", n), w.clock, client, access)
}

func (w *world) registry(push, pull string) {
	w.exec(`INSERT INTO image_registry_settings (id, provider, account, repository, push_token_ciphertext, pull_token_ciphertext, updated_at)
VALUES (true, 'ghcr', 'acme', 'repo', $1, $2, $3)`, push, pull, w.clock)
}

func (w *world) eventRegistry(n int, ct string) {
	w.exec(`INSERT INTO event_registry_secrets (id, event_id, host, username, token_ciphertext, created_at, updated_at)
VALUES ($1, $2, 'ghcr.io', 'u', $3, $4, $4)`, id(n), id(900+n), ct, w.clock)
}

func (w *world) vpn(n int, user uuid.UUID, scope string, ref uuid.NullUUID, ct string) {
	w.exec(`INSERT INTO user_vpn_configs (id, user_id, scope, scope_ref, config, created_at, updated_at) VALUES ($1, $2, $3, $4, $5, $6, $6)`,
		id(n), user, scope, ref, ct, w.clock)
}

func (w *world) version(n int, variantID uuid.UUID, doc string) {
	w.exec(`INSERT INTO exercises (id, name, description, tags, created_at, updated_at) VALUES ($1, $3, '', '{}', $2, $2)`, id(800+n), w.clock, fmt.Sprintf("exercise-%d", n))
	w.exec(`INSERT INTO exercise_versions (id, exercise_id, status, name, description, tags, variants, created_at) VALUES ($1, $2, 'draft', 'n', '', '{}', $3::jsonb, $4)`,
		id(n), id(800+n), doc, w.clock)
}

func (w *world) variants(n int) string {
	var raw []byte
	require.NoError(w.t, w.pool.QueryRow(context.Background(), `SELECT variants FROM exercise_versions WHERE id = $1`, id(n)).Scan(&raw))
	return string(raw)
}

func (w *world) usecase(store keyrotation.Store) *keyrotation.UseCase {
	return keyrotation.New(store, keyrotation.Rings{
		keyRotationRepo.FamilyPlatform: w.ring, keyRotationRepo.FamilyVPN: w.ring, keyRotationRepo.FamilyExercise: w.ring,
	})
}

// status is the full read of every stored value with its row context: the number of values that cannot be opened.
func (w *world) problems() int {
	rep, err := secretkeys.New(secretKeysRepo.New(w.pool), secretkeys.Ciphers{Platform: w.ring, VPN: w.ring, Exercise: w.ring}).Check(context.Background(), true)
	require.NoError(w.t, err)
	return rep.Problems()
}

func (w *world) byKey() map[string]int {
	rep, err := secretkeys.New(secretKeysRepo.New(w.pool), secretkeys.Ciphers{Platform: w.ring, VPN: w.ring, Exercise: w.ring}).Check(context.Background(), true)
	require.NoError(w.t, err)
	out := map[string]int{}
	for _, c := range rep.Columns {
		for k, v := range c.ByKey {
			out[k] += v
		}
		out["legacy"] += c.Legacy
	}
	return out
}

func keyOf(ct string) string { k, _ := secret.KeyID(ct); return k }

// hooked wraps the sources of a store so a test can act at the points a real database would race.
type hooked struct {
	keyrotation.Store
	beforeRows func(source string)
	beforeSwap func(source, rowID string)
}

func (h hooked) Sources(family string) []secret.Source {
	var out []secret.Source
	for _, s := range h.Store.Sources(family) {
		out = append(out, hookedSource{Source: s, h: h})
	}
	return out
}

type hookedSource struct {
	secret.Source
	h hooked
}

func (s hookedSource) Rows(ctx context.Context, after string, limit int) ([]secret.Row, error) {
	if s.h.beforeRows != nil {
		s.h.beforeRows(s.Name())
	}
	return s.Source.Rows(ctx, after, limit)
}

func (s hookedSource) Swap(ctx context.Context, rowID string, o, n []string) (bool, error) {
	if s.h.beforeSwap != nil {
		s.h.beforeSwap(s.Name(), rowID)
	}
	return s.Source.Swap(ctx, rowID, o, n)
}

// 12. a concurrent edit between the read and the write wins; the row is a conflict, not a failure.
func TestRun_ConcurrentEditWins(t *testing.T) {
	w := newWorld(t)
	w.smtp(1, w.seal(w.oldC, "p1", w.ctxs.SMTP(id(1))))
	edited := w.seal(w.ring, "edited", w.ctxs.SMTP(id(1)))
	once := sync.Once{}
	store := hooked{Store: w.repo, beforeSwap: func(string, string) {
		once.Do(func() { w.exec(`UPDATE mail_smtp_configs SET password_ciphertext = $2 WHERE id = $1`, id(1), edited) })
	}}
	rep, err := w.usecase(store).Run(context.Background(), keyRotationRepo.FamilyPlatform, keyrotation.Options{})
	require.NoError(t, err)
	require.Equal(t, edited, w.smtpValue(1), "the user's edit must survive")
	var conflicts, failed int
	for _, s := range rep.Sources {
		conflicts += s.Conflicts
		failed += s.Failed
	}
	require.Equal(t, 1, conflicts)
	require.Zero(t, failed)
}

// 13 and 24. readers and writers during the pass: at every barrier (before, between batches, after) every stored value
// opens, and the rows written meanwhile under the current key are all there at the end.
func TestRun_ReadersAndWritersDuringThePass(t *testing.T) {
	w := newWorld(t)
	for n := 1; n <= 5; n++ {
		w.smtp(n, w.seal(w.oldC, fmt.Sprintf("p%d", n), w.ctxs.SMTP(id(n))))
	}
	next := 100
	barriers := 0
	check := func(string) {
		barriers++
		require.Zero(t, w.problems(), "a row was unreadable at a barrier")
		if next < 104 { // a writer on the current key; bounded, or every page would bring a new row
			w.smtp(next, w.seal(w.ring, "late", w.ctxs.SMTP(id(next))))
			next++
		}
	}
	store := hooked{Store: w.repo, beforeRows: check}
	_, err := w.usecase(store).Run(context.Background(), keyRotationRepo.FamilyPlatform, keyrotation.Options{BatchSize: 2})
	require.NoError(t, err)
	require.Greater(t, barriers, 3)
	require.Zero(t, w.problems())
	for n := 100; n < next; n++ {
		_, err := w.ring.DecryptWithContext(w.smtpValue(n), w.ctxs.SMTP(id(n)))
		require.NoError(t, err, "a row written during the pass was lost or damaged")
	}
	for n := 1; n <= 5; n++ {
		require.Equal(t, "k2", keyOf(w.smtpValue(n)))
	}
}

// 14 and 27. one run per family: a second run of the same family is refused while the first is inside, another family
// is independent.
func TestRun_OneRunPerFamily(t *testing.T) {
	w := newWorld(t)
	w.smtp(1, w.seal(w.oldC, "p1", w.ctxs.SMTP(id(1))))
	w.vpn(1, id(50), "test", uuid.NullUUID{}, w.seal(w.oldC, "cfg", w.ctxs.VPN(id(50), "test", uuid.NullUUID{})))
	inside, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	store := hooked{Store: w.repo, beforeSwap: func(string, string) {
		once.Do(func() { close(inside); <-release })
	}}
	uc := w.usecase(store)
	done := make(chan error, 1)
	go func() {
		_, err := uc.Run(context.Background(), keyRotationRepo.FamilyPlatform, keyrotation.Options{})
		done <- err
	}()
	<-inside
	_, err := w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyPlatform, keyrotation.Options{})
	require.ErrorIs(t, err, secret.ErrAlreadyRunning)
	_, err = w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyVPN, keyrotation.Options{})
	require.NoError(t, err, "another family must run while the first one holds its lock")
	close(release)
	require.NoError(t, <-done)
	_, err = w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyPlatform, keyrotation.Options{})
	require.NoError(t, err, "the lock is released after the run")
}

const exerciseDoc = `[{"id":"%s","name":"v","extra":{"n":1.50,"list":[1,2,3]},"topology":{"devices":[
 {"name":"web","bootstrap":{"env":[{"name":"A","value":"%s","secret":true},{"name":"PLAIN","value":"plain","secret":false},{"name":"B","value":"%s","secret":true}]}},
 {"name":"db","bootstrap":{"env":[{"name":"C","value":"%s","secret":true}]}}]}}]`

// 15, 16, 18. a copy of a database with mixed key ids across every family: all that open move to the current key,
// the broken ones stay as they were and are listed, nothing is deleted, the ring is unchanged.
func TestRun_MixedDatabaseEveryFamily(t *testing.T) {
	w := newWorld(t)
	vid := id(7)
	env := func(c *secret.Cipher, plain, device, name string) string {
		return w.seal(c, plain, w.ctxs.ExerciseEnv(vid, device, name))
	}
	legacyOf := func(full string) string { return strings.SplitN(strings.TrimPrefix(full, "v1:"), ":", 2)[1] }
	legacySealer, err := secret.New(hexOld) // sealed as "default"
	require.NoError(t, err)

	w.smtp(1, w.seal(w.oldC, "s1", w.ctxs.SMTP(id(1))))
	w.smtp(2, w.seal(w.ring, "s2", w.ctxs.SMTP(id(2))))
	w.smtp(3, legacyOf(w.seal(legacySealer, "s3", w.ctxs.SMTP(id(3))))) // legacy, opens: the ring holds k1 = same key bytes
	w.smtp(4, w.seal(w.oldC, "s4", w.ctxs.SMTP(id(999))))               // context mismatch
	lost, err := secret.New("k0:" + strings.Repeat("33", 32))
	require.NoError(t, err)
	w.smtp(5, w.seal(lost, "s5", w.ctxs.SMTP(id(5)))) // sealed by a key that is gone
	w.agent(1, w.seal(w.oldC, "ck", w.ctxs.AgentKey(id(1))), w.seal(w.oldC, "ak", w.ctxs.AgentAccess(id(1))))
	w.registry(w.seal(w.oldC, "push", w.ctxs.RegistryPush), w.seal(w.ring, "pull", w.ctxs.RegistryPull))
	w.eventRegistry(1, w.seal(w.oldC, "tok", w.ctxs.EventRegistry(id(1))))
	user := id(60)
	ref := uuid.NullUUID{UUID: id(61), Valid: true}
	w.vpn(1, user, "event", ref, w.seal(w.oldC, "cfg", w.ctxs.VPN(user, "event", ref)))
	w.version(7, vid, fmt.Sprintf(exerciseDoc, vid, env(w.oldC, "a", "web", "A"), env(w.ring, "b", "web", "B"), env(w.oldC, "c", "db", "C")))
	// a version where one value does not open: the whole document must stay as it is
	badVid := id(8)
	badDoc := fmt.Sprintf(exerciseDoc, badVid, w.seal(w.oldC, "a", w.ctxs.ExerciseEnv(badVid, "web", "A")), w.seal(w.oldC, "b", w.ctxs.ExerciseEnv(badVid, "web", "B")),
		w.seal(w.oldC, "c", w.ctxs.ExerciseEnv(badVid, "db", "OTHER")))
	w.version(8, badVid, badDoc)
	badBefore := w.variants(8)
	goodBefore := w.variants(7)

	count := func(table string) (n int) {
		require.NoError(t, w.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n))
		return n
	}
	tables := []string{"mail_smtp_configs", "infrastructure_agents", "image_registry_settings", "event_registry_secrets", "user_vpn_configs", "exercise_versions"}
	rows := map[string]int{}
	for _, tb := range tables {
		rows[tb] = count(tb)
	}
	ids := w.ring.KeyIDs()

	uc := w.usecase(w.repo)
	var failed []secret.Failed
	byReason := map[secret.Reason]int{}
	for _, fam := range []string{keyRotationRepo.FamilyPlatform, keyRotationRepo.FamilyVPN, keyRotationRepo.FamilyExercise} {
		rep, runErr := uc.Run(context.Background(), fam, keyrotation.Options{BatchSize: 2})
		require.NoError(t, runErr)
		for _, s := range rep.Sources {
			failed = append(failed, s.FailedRows...)
			for k, v := range s.ByReason {
				byReason[k] += v
			}
		}
	}

	// every value that opened is now on k2 and still opens with its context
	for n := range []int{1, 2, 3} {
		n++
		require.Equal(t, "k2", keyOf(w.smtpValue(n)), "smtp %d", n)
	}
	require.Equal(t, "k1", keyOf(w.smtpValue(4)), "a value with another context is never rewritten")
	require.Equal(t, "k0", keyOf(w.smtpValue(5)), "a value of a lost key is never rewritten")
	require.Equal(t, badBefore, w.variants(8), "one value that does not open keeps the whole document")
	require.NotEqual(t, goodBefore, w.variants(7))

	// the other parts of the document are unchanged, only the three values differ
	var before, after []any
	require.NoError(t, json.Unmarshal([]byte(goodBefore), &before))
	require.NoError(t, json.Unmarshal([]byte(w.variants(7)), &after))
	flat := w.variants(7)
	require.NotContains(t, flat, ":k1:")
	require.Contains(t, flat, `"n": 1.50`, "numbers inside the document stay as written")

	// failures are listed with source, row and a reason class
	reasons := map[secret.Reason]int{secret.ReasonAAD: 2, secret.ReasonUnknownKey: 1}
	require.Equal(t, reasons, map[secret.Reason]int{secret.ReasonAAD: byReason[secret.ReasonAAD], secret.ReasonUnknownKey: byReason[secret.ReasonUnknownKey]})
	require.Len(t, failed, 3)

	// 18. nothing was deleted, no key was touched
	for _, tb := range tables {
		require.Equal(t, rows[tb], count(tb), tb)
	}
	require.Equal(t, ids, w.ring.KeyIDs())

	// a second run changes nothing
	for _, fam := range []string{keyRotationRepo.FamilyPlatform, keyRotationRepo.FamilyVPN, keyRotationRepo.FamilyExercise} {
		rep, runErr := uc.Run(context.Background(), fam, keyrotation.Options{})
		require.NoError(t, runErr)
		for _, s := range rep.Sources {
			require.Zero(t, s.Done, s.Source)
		}
	}
	require.Equal(t, 3, w.problemsFound(), "the three broken values are still the only ones that do not open")
}

func (w *world) problemsFound() int { return w.problems() }

// 17, 25, 26. the progress table: states and counts, failed counts per reason, no value anywhere in it or in the report.
func TestRun_ProgressTableHoldsCountsOnly(t *testing.T) {
	w := newWorld(t)
	plainBad := "very-secret-plaintext"
	bad := w.seal(w.oldC, plainBad, w.ctxs.SMTP(id(999)))
	w.smtp(1, w.seal(w.oldC, "p1", w.ctxs.SMTP(id(1))))
	w.smtp(2, bad)
	rep, err := w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyPlatform, keyrotation.Options{})
	require.NoError(t, err)

	got, err := w.repo.Progress(context.Background())
	require.NoError(t, err)
	var smtp *keyRotationRepo.StoredProgress
	for i := range got {
		if got[i].Source == "mail_smtp_configs.password_ciphertext" {
			smtp = &got[i]
		}
	}
	require.NotNil(t, smtp)
	require.Equal(t, keyRotationRepo.StateFinished, smtp.State)
	require.Equal(t, "k2", smtp.KeyID)
	require.Equal(t, 2, smtp.Total)
	require.Equal(t, 1, smtp.Done)
	require.Equal(t, 1, smtp.Failed)
	require.Equal(t, map[string]int{string(secret.ReasonAAD): 1}, smtp.FailedByReason)

	// the table and the report hold no plaintext and no ciphertext
	var dump string
	require.NoError(t, w.pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(t::text, ' '), '') FROM key_rotation_progress t`).Scan(&dump))
	out := dump + fmt.Sprintf("%+v", rep)
	for _, leak := range []string{plainBad, bad, strings.TrimPrefix(bad, "v1:k1:"), w.smtpValue(1)} {
		require.NotContains(t, out, leak)
	}
	require.Contains(t, out, id(2).String(), "the failed row is named by id")

	// a dry run leaves the table as it was
	before := dump
	_, err = w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyPlatform, keyrotation.Options{DryRun: true})
	require.NoError(t, err)
	var after string
	require.NoError(t, w.pool.QueryRow(context.Background(), `SELECT coalesce(string_agg(t::text, ' '), '') FROM key_rotation_progress t`).Scan(&after))
	require.Equal(t, before, after)
}

// 17. an interrupted run is recorded as interrupted with the counts so far, and a new run finishes it.
func TestRun_InterruptedIsRecordedAndResumed(t *testing.T) {
	w := newWorld(t)
	for n := 1; n <= 4; n++ {
		w.smtp(n, w.seal(w.oldC, "p", w.ctxs.SMTP(id(n))))
	}
	ctx, cancel := context.WithCancel(context.Background())
	swaps := 0
	store := hooked{Store: w.repo, beforeSwap: func(string, string) {
		swaps++
		if swaps == 3 {
			cancel()
		}
	}}
	_, err := w.usecase(store).Run(ctx, keyRotationRepo.FamilyPlatform, keyrotation.Options{BatchSize: 2})
	require.ErrorIs(t, err, context.Canceled)
	got, err := w.repo.Progress(context.Background())
	require.NoError(t, err)
	require.Equal(t, keyRotationRepo.StateInterrupted, got[0].State)
	require.Zero(t, w.problems(), "every row opens after an interruption")

	_, err = w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyPlatform, keyrotation.Options{BatchSize: 2})
	require.NoError(t, err)
	got, err = w.repo.Progress(context.Background())
	require.NoError(t, err)
	require.Equal(t, keyRotationRepo.StateFinished, got[0].State)
	require.Equal(t, 0, w.byKey()["k1"])
}

func TestRun_UnknownFamilyAndMissingKey(t *testing.T) {
	w := newWorld(t)
	_, err := w.usecase(w.repo).Run(context.Background(), "nope", keyrotation.Options{})
	require.ErrorIs(t, err, keyrotation.ErrUnknownFamily)
	uc := keyrotation.New(w.repo, keyrotation.Rings{})
	_, err = uc.Run(context.Background(), keyRotationRepo.FamilyVPN, keyrotation.Options{})
	require.ErrorIs(t, err, keyrotation.ErrFamilyKeyMissing)
}
