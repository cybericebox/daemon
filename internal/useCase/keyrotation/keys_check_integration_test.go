package keyrotation_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/app"
	"github.com/cybericebox/daemon/internal/delivery/repository/keyRotationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/secretKeysRepo"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
	"github.com/cybericebox/daemon/internal/useCase/secretkeys"
	"github.com/cybericebox/daemon/pkg/secret"
)

func (w *world) ciphers() secretkeys.Ciphers {
	return secretkeys.Ciphers{Platform: w.ring, VPN: w.ring, Exercise: w.ring}
}

func (w *world) checkReport() secretkeys.Report {
	w.t.Helper()
	rep, err := app.KeysCheckReport(w.ctx, w.pool, w.ciphers())
	require.NoError(w.t, err)
	return rep
}

// river creates the job tables, which the queue's own migration makes when the backend starts.
func (w *world) river() {
	w.t.Helper()
	migrator, err := rivermigrate.New(riverpgxv5.New(w.db.Pool), nil)
	require.NoError(w.t, err)
	_, err = migrator.Migrate(w.ctx, rivermigrate.DirectionUp, nil)
	require.NoError(w.t, err)
}

func (w *world) rotate(family string) {
	w.t.Helper()
	_, err := w.usecase(w.repo).Run(w.ctx, family, keyrotation.Options{})
	require.NoError(w.t, err)
}

// 11. the scan reads in a read-only transaction: nothing can be written through it, and it leaves no trace (no progress row,
// no lock).
func TestKeysCheck_ReadsInAReadOnlyTransactionAndWritesNothing(t *testing.T) {
	w := newWorld(t)
	w.smtp(1, w.seal(w.ring, "p1", w.ctxs.SMTP(id(1))))

	tx, err := secretKeysRepo.Snapshot(w.ctx, w.pool)
	require.NoError(t, err)
	var readOnly string
	require.NoError(t, tx.QueryRow(w.ctx, `SHOW transaction_read_only`).Scan(&readOnly))
	require.Equal(t, "on", readOnly)
	_, err = tx.Exec(w.ctx, `UPDATE mail_smtp_configs SET host = 'changed'`)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "25006", pgErr.Code, "a write through the snapshot is refused by the server")
	require.NoError(t, tx.Rollback(w.ctx))

	before := w.smtpValue(1)
	_ = w.checkReport()
	require.Equal(t, before, w.smtpValue(1))
	var progress, locks int
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT count(*) FROM key_rotation_progress`).Scan(&progress))
	require.NoError(t, w.pool.QueryRow(w.ctx, `SELECT count(*) FROM pg_locks WHERE locktype = 'advisory'`).Scan(&locks))
	require.Zero(t, progress, "a check leaves no progress row")
	require.Zero(t, locks, "a check holds no advisory lock")
}

// hookTx runs a function once, right after the first statement of the snapshot began.
type hookTx struct {
	pgx.Tx
	after func()
	done  bool
}

func (h *hookTx) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	rows, err := h.Tx.Query(ctx, sql, args...)
	if !h.done {
		h.done = true
		h.after()
	}
	return rows, err
}

// 12. one snapshot: a value sealed by the old key and a running progress row that appear while the check is reading are not
// in its answer; a check made afterwards sees both.
func TestKeysCheck_AllTablesComeFromOneSnapshot(t *testing.T) {
	w := newWorld(t)
	w.smtp(1, w.seal(w.ring, "p1", w.ctxs.SMTP(id(1))))

	tx, err := secretKeysRepo.Snapshot(w.ctx, w.pool)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.WithoutCancel(w.ctx)) }()
	hooked := &hookTx{Tx: tx, after: func() {
		w.smtp(2, w.seal(w.oldC, "late", w.ctxs.SMTP(id(2))))
		require.NoError(t, w.repo.SaveProgress(w.ctx, secret.Progress{Family: "platform", Source: "mail_smtp_configs.password_ciphertext", KeyID: "k2", UpdatedAt: w.clock},
			keyRotationRepo.StateRunning, w.clock))
	}}
	rep, err := app.KeysCheckReportOn(w.ctx, w.pool, hooked, w.ciphers())
	require.NoError(t, err)
	require.True(t, hooked.done)
	v, err := rep.Verdict(secretkeys.FamilyPlatform, "k1")
	require.NoError(t, err)
	require.True(t, v.Retirable, "the answer is that of the snapshot, not a mix of before and after: %v", v.Blockers)

	v, err = w.checkReport().Verdict(secretkeys.FamilyPlatform, "k1")
	require.NoError(t, err)
	require.False(t, v.Retirable, "a check made after the change sees it")
	text := strings.Join(v.Blockers, "\n")
	require.Contains(t, text, "1 value(s) in mail_smtp_configs.password_ciphertext are sealed by it")
	require.Contains(t, text, "is interrupted (process gone)", "a running row with no process is shown as interrupted")
}

// 7. short-lived values: an unexpired temporal code and an unfinished job block the key that seals them; expired codes and
// finished jobs do not.
func TestKeysCheck_ShortLivedValuesBlockOnlyWhileTheyLive(t *testing.T) {
	w := newWorld(t)
	w.river()
	w.smtp(1, w.seal(w.ring, "p1", w.ctxs.SMTP(id(1))))
	sealedOld := w.seal(w.oldC, "queued", []byte("notify-payload:x"))

	insertCode := func(n int, expires time.Time) {
		w.exec(`INSERT INTO temporal_codes (id, code, type, data, expires_at) VALUES ($1, $2, 1, jsonb_build_object('payload', $3::text), $4)`,
			id(n), fmt.Sprintf("code-%d", n), sealedOld, expires)
	}
	insertJob := func(state string) {
		w.exec(`INSERT INTO river_job (state, kind, args, max_attempts, queue, priority, finalized_at)
VALUES ($1::river_job_state, 'exercise_import', jsonb_build_object('sealed_password', $2::text), 3, 'default', 1,
        CASE WHEN $1::river_job_state IN ('completed', 'cancelled', 'discarded') THEN now() END)`, state, sealedOld)
	}

	insertCode(1, time.Now().Add(-time.Hour)) // expired
	insertJob("completed")
	insertJob("cancelled")
	insertJob("discarded")
	v, err := w.checkReport().Verdict(secretkeys.FamilyPlatform, "k1")
	require.NoError(t, err)
	require.True(t, v.Retirable, "expired codes and finished jobs do not hold the key: %v", v.Blockers)

	insertCode(2, time.Now().Add(time.Hour))
	v, err = w.checkReport().Verdict(secretkeys.FamilyPlatform, "k1")
	require.NoError(t, err)
	require.False(t, v.Retirable)
	require.Contains(t, strings.Join(v.Blockers, "\n"), "1 short-lived value(s) in temporal_codes.data (not expired)")

	w.exec(`DELETE FROM temporal_codes`)
	for _, state := range []string{"available", "running", "retryable", "scheduled"} {
		w.exec(`DELETE FROM river_job`)
		insertJob(state)
		v, err = w.checkReport().Verdict(secretkeys.FamilyPlatform, "k1")
		require.NoError(t, err)
		require.False(t, v.Retirable, state)
		require.Contains(t, strings.Join(v.Blockers, "\n"), "river_job.args (not finished)", state)
	}
	// the current key's own short-lived values never block the old one
	w.exec(`DELETE FROM river_job`)
	insertJob("running")
	w.exec(`UPDATE river_job SET args = jsonb_build_object('sealed_password', $1::text)`, w.seal(w.ring, "queued", []byte("x")))
	v, err = w.checkReport().Verdict(secretkeys.FamilyPlatform, "k1")
	require.NoError(t, err)
	require.True(t, v.Retirable, "%v", v.Blockers)
}

// 14. end to end on a database with mixed key ids: not retirable while old-key, legacy and broken values are there, and
// retirable after they are fixed and the family was re-encrypted; the time of the last move comes from the progress rows.
func TestKeysCheck_MixedDatabaseBecomesRetirableAfterTheRotation(t *testing.T) {
	w := newWorld(t)
	legacySealer, err := secret.New(hexOld)
	require.NoError(t, err)
	legacy := strings.SplitN(strings.TrimPrefix(w.seal(legacySealer, "s3", w.ctxs.SMTP(id(3))), "v1:"), ":", 2)[1]
	w.smtp(1, w.seal(w.oldC, "s1", w.ctxs.SMTP(id(1))))
	w.smtp(2, w.seal(w.ring, "s2", w.ctxs.SMTP(id(2))))
	w.smtp(3, legacy)
	w.smtp(4, w.seal(w.oldC, "s4", w.ctxs.SMTP(id(999)))) // context does not match: cannot be opened

	v, err := w.checkReport().Verdict(secretkeys.FamilyPlatform, "k1")
	require.NoError(t, err)
	require.False(t, v.Retirable)
	text := strings.Join(v.Blockers, "\n")
	require.Contains(t, text, "cannot be opened (aad_mismatch: 1)")
	require.Contains(t, text, "older form without a key id")
	require.Contains(t, text, "sealed by it")

	w.rotate(keyRotationRepo.FamilyPlatform)
	v, err = w.checkReport().Verdict(secretkeys.FamilyPlatform, "k1")
	require.NoError(t, err)
	require.False(t, v.Retirable, "the broken value and the run that finished with failures still hold the key")
	require.Contains(t, strings.Join(v.Blockers, "\n"), "finished_with_failures")

	w.exec(`DELETE FROM mail_smtp_configs WHERE id = $1`, id(4)) // the owner fixes the broken value
	w.rotate(keyRotationRepo.FamilyPlatform)
	rep := w.checkReport()
	v, err = rep.Verdict(secretkeys.FamilyPlatform, "k1")
	require.NoError(t, err)
	require.True(t, v.Retirable, "%v", v.Blockers)
	require.False(t, v.LeftAt.IsZero(), "the time the last value left the key comes from the progress rows")
	require.Contains(t, v.BackupSentence(), "when the last value left this key")

	var out strings.Builder
	secretkeys.WriteVerdict(&out, v, func(secretkeys.Family, string) string { return "elsewhere" }, nil)
	require.Contains(t, out.String(), "RETIRABLE")
	for _, leak := range []string{"s1", legacy, strings.TrimPrefix(w.smtpValue(1), "v1:k2:"), w.smtpValue(1)} {
		require.NotContains(t, out.String(), leak, "the answer holds no value and no part of a ciphertext")
	}
}
