// Package labCheckRepo keeps the results of the laboratory checks of a lab and the manual runs asked for.
package labCheckRepo

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labCheckModel "github.com/cybericebox/daemon/internal/model/labCheck"
)

type Repository struct{ db postgres.DBTX }

func New(db postgres.DBTX) *Repository { return &Repository{db: db} }

// Record stores a result that changed a check's state (passed or not passed). first_passed_at is set the first time a pass
// is seen and never goes back; changed_at moves only when the state differs from the stored one.
func (r *Repository) Record(ctx context.Context, res labCheckModel.Result, now time.Time) error {
	_, err := r.db.Exec(ctx, `
INSERT INTO lab_check_results (lab_group_name, lab_name, check_id, state, first_passed_at, changed_at, value, detail, observed_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (lab_group_name, lab_name, check_id) DO UPDATE SET
    state           = EXCLUDED.state,
    first_passed_at = COALESCE(lab_check_results.first_passed_at, EXCLUDED.first_passed_at),
    changed_at      = CASE WHEN lab_check_results.state <> EXCLUDED.state THEN EXCLUDED.changed_at ELSE lab_check_results.changed_at END,
    value           = EXCLUDED.value,
    detail          = EXCLUDED.detail,
    observed_at     = EXCLUDED.observed_at,
    updated_at      = EXCLUDED.updated_at`,
		res.Group, res.Lab, res.CheckID, int16(res.State), res.FirstPassedAt, res.ChangedAt, res.Value, res.Detail, res.ObservedAt, now)
	return err
}

// NoteDetail replaces the detail of a check that has a stored result and keeps its state: the laboratory could not make the
// check (an error), so the previous state stands and only the reason for staff changes. A check without a result is ignored.
func (r *Repository) NoteDetail(ctx context.Context, group, lab, checkID, detail string, now time.Time) error {
	_, err := r.db.Exec(ctx, `UPDATE lab_check_results SET detail = $4, updated_at = $5
WHERE lab_group_name = $1 AND lab_name = $2 AND check_id = $3`, group, lab, checkID, detail, now)
	return err
}

// List returns the results of one lab.
func (r *Repository) List(ctx context.Context, group, lab string) ([]labCheckModel.Result, error) {
	rows, err := r.db.Query(ctx, `
SELECT check_id, state, first_passed_at, changed_at, value, detail, observed_at
FROM lab_check_results WHERE lab_group_name = $1 AND lab_name = $2 ORDER BY check_id`, group, lab)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []labCheckModel.Result
	for rows.Next() {
		res := labCheckModel.Result{Group: group, Lab: lab}
		var state int16
		if err = rows.Scan(&res.CheckID, &state, &res.FirstPassedAt, &res.ChangedAt, &res.Value, &res.Detail, &res.ObservedAt); err != nil {
			return nil, err
		}
		res.State = labCheckModel.State(state)
		out = append(out, res)
	}
	return out, rows.Err()
}

// Purge drops the results nobody wrote to since the cutoff (a lab that is gone) and the runs older than it.
func (r *Repository) Purge(ctx context.Context, before time.Time) error {
	if _, err := r.db.Exec(ctx, `DELETE FROM lab_check_results WHERE updated_at < $1`, before); err != nil {
		return err
	}
	_, err := r.db.Exec(ctx, `DELETE FROM lab_check_runs WHERE asked_at < $1`, before)
	return err
}

// DropLab forgets the results of a lab that was removed.
func (r *Repository) DropLab(ctx context.Context, group, lab string) error {
	_, err := r.db.Exec(ctx, `DELETE FROM lab_check_results WHERE lab_group_name = $1 AND lab_name = $2`, group, lab)
	return err
}

// AskRun records a manual run of a task's checks unless the lab group already asked limit times for this task within the
// window. When refused it says when the oldest ask in the window leaves it.
func (r *Repository) AskRun(ctx context.Context, group, taskKey string, window time.Duration, limit int, now time.Time) (allowed bool, retryAfter time.Duration, err error) {
	tag, err := r.db.Exec(ctx, `
INSERT INTO lab_check_runs (lab_group_name, task_key, asked_at)
SELECT $1, $2, $3
WHERE (SELECT count(*) FROM lab_check_runs WHERE lab_group_name = $1 AND task_key = $2 AND asked_at > $4) < $5`,
		group, taskKey, now, now.Add(-window), limit)
	if err != nil {
		return false, 0, err
	}
	if tag.RowsAffected() == 1 {
		return true, 0, nil
	}
	var oldest time.Time
	err = r.db.QueryRow(ctx, `SELECT min(asked_at) FROM lab_check_runs WHERE lab_group_name = $1 AND task_key = $2 AND asked_at > $3`,
		group, taskKey, now.Add(-window)).Scan(&oldest)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return false, 0, err
	}
	retryAfter = oldest.Add(window).Sub(now)
	if retryAfter < time.Second {
		retryAfter = time.Second
	}
	return false, retryAfter, nil
}
