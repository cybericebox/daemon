package keyrotation_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/keyRotationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
	"github.com/cybericebox/daemon/pkg/secret"
)

// docWithoutSecret is a version document that holds no secret env value.
const docWithoutSecret = `[{"id":"%s","name":"v","topology":{"devices":[{"name":"web","bootstrap":{"env":[{"name":"A","value":"plain","secret":false}]}}]}}]`

// pauseInSwap makes the first exercise swap stop while it holds its locks, and returns what the test needs to drive it.
type swapPause struct {
	locked, release chan struct{}
	done            chan error
}

func (w *world) pauseFirstSwap() *swapPause {
	p := &swapPause{locked: make(chan struct{}), release: make(chan struct{}), done: make(chan error, 1)}
	var once sync.Once
	w.repo.AfterLock = func() {
		once.Do(func() {
			close(p.locked)
			<-p.release
		})
	}
	return p
}

func (w *world) startPass(p *swapPause) {
	go func() {
		_, err := w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyExercise, keyrotation.Options{})
		p.done <- err
	}()
	<-p.locked
}

func (p *swapPause) finish(t *testing.T) {
	close(p.release)
	select {
	case err := <-p.done:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the pass did not finish")
	}
}

func (w *world) event() uuid.UUID {
	w.t.Helper()
	ctx := context.Background()
	user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "keys-test@test.test", w.clock)
	user.Role = rbac.RoleSuperAdmin
	_, err := userRepo.New(w.db.Queries).Create(ctx, user)
	require.NoError(w.t, err)
	ev, err := eventModel.NewEvent("keyspin", "Pinned", w.clock, w.clock.Add(time.Hour), user.ID, w.clock)
	require.NoError(w.t, err)
	ev, err = eventRepo.New(w.db.Queries).Create(ctx, ev)
	require.NoError(w.t, err)
	return ev.ID
}

// 1 and 2. while a swap holds its locks: a writer that only needs the exercise row for a foreign key (pinning it to an
// event) does not wait, a version without a secret is not locked at all, and an application writer that takes the
// exercise row for update (the lock of every save) does wait.
func TestSwap_LocksDoNotBlockForeignKeyWritersOrVersionsWithoutSecrets(t *testing.T) {
	w := newWorld(t)
	vid := id(7)
	ct := w.seal(w.oldC, "shared", w.ctxs.ExerciseEnv(vid, "web", "A"))
	w.exercisePair(vid, map[int]string{
		21: fmt.Sprintf(pairDoc, vid, "v", ct),
		22: fmt.Sprintf(pairDoc, vid, "v", ct),
		23: fmt.Sprintf(docWithoutSecret, vid), // a checkpoint without any secret
	}, 21, 22)
	eventID := w.event()

	p := w.pauseFirstSwap()
	w.startPass(p)

	short, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// pin the exercise to an event: foreign keys to the exercise and to its version take FOR KEY SHARE
	_, err := w.db.Queries.CreateEventExercise(short, postgres.CreateEventExerciseParams{
		ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: id(850), ExerciseVersionID: id(21), Revision: 1, CreatedAt: w.clock,
	})
	require.NoError(t, err, "a pin to an event must not wait for the swap")

	// a version without a secret is not locked by the swap
	lockTimeout := func(ctx context.Context, sql string, args ...any) error {
		conn, acqErr := w.pool.Acquire(ctx)
		require.NoError(t, acqErr)
		defer conn.Release()
		_, execErr := conn.Exec(ctx, `SET lock_timeout = '400ms'`)
		require.NoError(t, execErr)
		_, execErr = conn.Exec(ctx, sql, args...)
		return execErr
	}
	require.NoError(t, lockTimeout(short, `UPDATE exercise_versions SET admin_note = 'edited' WHERE id = $1`, id(23)),
		"a version without a secret must not be locked")

	// the lock of an application save (the exercise row FOR UPDATE) waits for the swap
	err = lockTimeout(short, `SELECT id FROM exercises WHERE id = $1 FOR UPDATE`, id(850))
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "55P03", pgErr.Code, "a save of the exercise must wait for the swap (lock not available)")

	p.finish(t)
}

// 3. the race: a copy of the published version into the draft is made by a single statement that started before the pass
// committed, so it copies the OLD ciphertext after the published row was rotated. The next pass must end with equal
// bytes, not with a fresh nonce for the draft.
func TestRun_DraftCopiedFromStaleReadEndsEqual(t *testing.T) {
	w := newWorld(t)
	vid := id(7)
	ct := w.seal(w.oldC, "shared", w.ctxs.ExerciseEnv(vid, "web", "A"))
	w.exercisePair(vid, map[int]string{
		21: fmt.Sprintf(pairDoc, vid, "v", ct),
		22: fmt.Sprintf(docWithoutSecret, vid),
	}, 21, 22)

	p := w.pauseFirstSwap()
	w.startPass(p)

	// RestoreVersionPreservingDraft copies the published version into the draft; it blocks on the exercise lock.
	restored := make(chan error, 1)
	go func() {
		_, err := w.db.Queries.RestoreVersionPreservingDraft(context.Background(), postgres.RestoreVersionPreservingDraftParams{
			ExerciseID: id(850), SourceID: id(21), CheckpointID: id(30), CreatedAt: w.clock,
		})
		restored <- err
	}()
	require.Eventually(t, func() bool {
		var blocked bool
		require.NoError(t, w.pool.QueryRow(context.Background(),
			`SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname = current_database() AND query LIKE '%locked_draft%' AND cardinality(pg_blocking_pids(pid)) > 0)`).Scan(&blocked))
		return blocked
	}, 10*time.Second, 5*time.Millisecond, "the copy must be waiting for the swap's lock")
	p.finish(t)
	require.NoError(t, <-restored)

	// the race happened: the draft holds the old ciphertext that was copied from the stale read
	require.Equal(t, "changed", w.exerciseStatus(), "the stale copy makes the draft differ in bytes from the rotated published version")
	require.Contains(t, w.variants(22), ":k1:")

	w.repo.AfterLock = nil
	_, err := w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyExercise, keyrotation.Options{})
	require.NoError(t, err)
	require.Equal(t, "published", w.exerciseStatus(), "equal content must end with equal bytes")
	require.Equal(t, w.variants(21), w.variants(22))
}

// A value that was edited (another plaintext under the same context) is never replaced by the other version's ciphertext.
func TestRun_EditedSecretIsNotReusedFromAnotherVersion(t *testing.T) {
	w := newWorld(t)
	vid := id(7)
	published := w.seal(w.oldC, "published-value", w.ctxs.ExerciseEnv(vid, "web", "A"))
	edited := w.seal(w.oldC, "edited-value", w.ctxs.ExerciseEnv(vid, "web", "A"))
	w.exercisePair(vid, map[int]string{21: fmt.Sprintf(pairDoc, vid, "v", published), 22: fmt.Sprintf(pairDoc, vid, "v", edited)}, 21, 22)
	_, err := w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyExercise, keyrotation.Options{})
	require.NoError(t, err)
	require.Equal(t, "changed", w.exerciseStatus())
	secretOf := func(n int) string {
		var out string
		require.NoError(t, w.pool.QueryRow(context.Background(),
			`SELECT variants #>> '{0,topology,devices,0,bootstrap,env,0,value}' FROM exercise_versions WHERE id = $1`, id(n)).Scan(&out))
		return out
	}
	require.NotEqual(t, secretOf(21), secretOf(22))
	for n, want := range map[int]string{21: "published-value", 22: "edited-value"} {
		plain, openErr := w.ring.DecryptWithContext(secretOf(n), w.ctxs.ExerciseEnv(vid, "web", "A"))
		require.NoError(t, openErr)
		require.Equal(t, want, string(plain))
	}
}

// 4. a sibling that is reported failed (another of its values cannot be opened) still receives the shared value that
// the swap of its sibling moved; the value that cannot be opened is never changed, and the report says partly rotated.
func TestRun_FailedSiblingIsPartlyRotatedAndTheBadValueIsNeverChanged(t *testing.T) {
	w := newWorld(t)
	vid := id(7)
	shared := w.seal(w.oldC, "shared", w.ctxs.ExerciseEnv(vid, "web", "A"))
	bad := w.seal(w.oldC, "bad", w.ctxs.ExerciseEnv(vid, "web", "OTHER")) // sealed for another env name: does not open as B
	const two = `[{"id":"%s","name":"v","topology":{"devices":[{"name":"web","bootstrap":{"env":[
 {"name":"A","value":"%s","secret":true},{"name":"B","value":"%s","secret":true}]}}]}}]`
	w.exercisePair(vid, map[int]string{
		21: fmt.Sprintf(pairDoc, vid, "v", shared),
		22: fmt.Sprintf(two, vid, shared, bad),
	}, 21, 22)

	rep, err := w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyExercise, keyrotation.Options{})
	require.NoError(t, err)
	require.Equal(t, 1, rep.Failed())
	var failed secret.Failed
	for _, s := range rep.Sources {
		for _, f := range s.FailedRows {
			failed = f
		}
	}
	require.Equal(t, id(22).String(), failed.RowID)
	require.True(t, failed.PartlyRotated, "the draft got the shared value, so it is not untouched")
	require.Contains(t, w.variants(22), bad, "the value that cannot be opened is exactly as it was")
	require.NotContains(t, w.variants(22), shared, "the shared value moved with its sibling")

	var out strings.Builder
	rep.Write(&out)
	require.Contains(t, out.String(), "partly rotated")
	require.Contains(t, out.String(), "never changed")
	require.NotContains(t, out.String(), "left untouched")
}

// 8. equal ciphertext bytes under a DIFFERENT context (another variant id, device or env name) are never touched: they
// were not readable there, and moving them would be a write the job has no right to make.
func TestRun_EqualBytesUnderAnotherContextAreNeverTouched(t *testing.T) {
	w := newWorld(t)
	vid, other := id(7), id(8)
	ct := w.seal(w.oldC, "shared", w.ctxs.ExerciseEnv(vid, "web", "A"))
	copyDoc := fmt.Sprintf(`[
 {"id":"%[1]s","name":"v","topology":{"devices":[{"name":"web","bootstrap":{"env":[{"name":"A","value":"%[3]s","secret":true}]}}]}},
 {"id":"%[2]s","name":"w","topology":{"devices":[{"name":"web","bootstrap":{"env":[{"name":"A","value":"%[3]s","secret":true}]}}]}},
 {"id":"%[1]s","name":"x","topology":{"devices":[{"name":"db","bootstrap":{"env":[{"name":"A","value":"%[3]s","secret":true}]}}]}},
 {"id":"%[1]s","name":"y","topology":{"devices":[{"name":"web","bootstrap":{"env":[{"name":"Z","value":"%[3]s","secret":true}]}}]}}]`, vid, other, ct)
	// the first entry of the copy is the same context as the published value; the other three differ in variant id, device
	// and env name. Keep the context-different ones only, so no readable value is in the row.
	onlyOthers := fmt.Sprintf(`[
 {"id":"%[2]s","name":"w","topology":{"devices":[{"name":"web","bootstrap":{"env":[{"name":"A","value":"%[3]s","secret":true}]}}]}},
 {"id":"%[1]s","name":"x","topology":{"devices":[{"name":"db","bootstrap":{"env":[{"name":"A","value":"%[3]s","secret":true}]}}]}},
 {"id":"%[1]s","name":"y","topology":{"devices":[{"name":"web","bootstrap":{"env":[{"name":"Z","value":"%[3]s","secret":true}]}}]}}]`, vid, other, ct)
	_ = copyDoc
	w.exercisePair(vid, map[int]string{21: fmt.Sprintf(pairDoc, vid, "v", ct), 23: onlyOthers}, 21, 21)
	before := w.variants(23)

	rep, err := w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyExercise, keyrotation.Options{})
	require.NoError(t, err)
	require.Equal(t, before, w.variants(23), "values under another context are byte for byte as they were")
	require.Contains(t, w.variants(23), ct)
	require.Equal(t, "k2", keyOf(firstSecret(t, w, 21)))
	require.Equal(t, 1, rep.Failed(), "the row with three unreadable values is the only failure")
	for _, s := range rep.Sources {
		for _, f := range s.FailedRows {
			require.False(t, f.PartlyRotated, "nothing of it was moved")
		}
	}
}

func firstSecret(t *testing.T, w *world, n int) string {
	t.Helper()
	var out string
	require.NoError(t, w.pool.QueryRow(context.Background(),
		`SELECT variants #>> '{0,topology,devices,0,bootstrap,env,0,value}' FROM exercise_versions WHERE id = $1`, id(n)).Scan(&out))
	return out
}

// The stale-batch path: the published row and its draft are in one batch; the draft was rewritten together with the
// published row, so its own swap is recognised (not a conflict). A real edit of the draft in between is still a conflict
// and survives.
func TestRun_StaleBatchRowIsRecognisedAndARealEditStillWins(t *testing.T) {
	w := newWorld(t)
	vid := id(7)
	ct := w.seal(w.oldC, "shared", w.ctxs.ExerciseEnv(vid, "web", "A"))
	w.exercisePair(vid, map[int]string{21: fmt.Sprintf(pairDoc, vid, "v", ct), 22: fmt.Sprintf(pairDoc, vid, "v", ct)}, 21, 22)
	rep, err := w.usecase(w.repo).Run(context.Background(), keyRotationRepo.FamilyExercise, keyrotation.Options{BatchSize: 100})
	require.NoError(t, err)
	for _, s := range rep.Sources {
		require.Zero(t, s.Conflicts)
		require.Zero(t, s.Failed)
	}
	require.Equal(t, 2, rep.Sources[0].Done, "both rows are counted as moved")

	// a second exercise: the draft is edited by someone right after the published row's swap
	w2 := newWorld(t)
	ct2 := w2.seal(w2.oldC, "shared", w2.ctxs.ExerciseEnv(vid, "web", "A"))
	w2.exercisePair(vid, map[int]string{21: fmt.Sprintf(pairDoc, vid, "v", ct2), 22: fmt.Sprintf(pairDoc, vid, "v", ct2)}, 21, 22)
	edited := w2.seal(w2.ring, "edited-by-user", w2.ctxs.ExerciseEnv(vid, "web", "A"))
	store := hooked{Store: w2.repo, beforeSwap: func(_, rowID string) {
		if rowID == id(22).String() {
			w2.exec(`UPDATE exercise_versions SET variants = jsonb_set(variants, '{0,topology,devices,0,bootstrap,env,0,value}', to_jsonb($2::text)) WHERE id = $1`, id(22), edited)
		}
	}}
	rep, err = w2.usecase(store).Run(context.Background(), keyRotationRepo.FamilyExercise, keyrotation.Options{BatchSize: 100})
	require.NoError(t, err)
	require.Equal(t, 1, rep.Sources[0].Conflicts, "an edit that is not the swap's own write is a conflict")
	require.Equal(t, edited, firstSecret(t, w2, 22), "the edit survives")
}
