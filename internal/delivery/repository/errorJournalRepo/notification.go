package errorJournalRepo

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

// Notification intent and River command always commit together with capture/target expansion.
func (r *Repository) enqueueNotification(ctx context.Context, tx pgx.Tx, groupID uuid.UUID, p *errorJournalUseCase.NotificationPlan) error {
	id, err := uuid.NewV7()
	if err != nil {
		return err
	}
	if p.ThrottleKind != "" {
		// One message per cooldown for the whole kind, across replicas: the lock serializes the decisions of
		// concurrent captures (it is released with the transaction, after the claim below is visible).
		const lockSQL = `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`
		kind := string(p.ThrottleKind)
		if _, lockErr := tx.Exec(ctx, lockSQL, "error-journal-kind:"+kind); lockErr != nil {
			return lockErr
		}
		var recent bool
		const recentSQL = `SELECT EXISTS(SELECT 1 FROM error_groups WHERE kind = $1 AND notification_enqueued_at > $2)`
		row := tx.QueryRow(ctx, recentSQL, kind, p.ThrottleCutoff)
		if scanErr := row.Scan(&recent); scanErr != nil {
			return scanErr
		}
		if recent {
			return nil
		}
	}
	n, err := postgres.New(tx).EnqueueErrorNotification(ctx, postgres.EnqueueErrorNotificationParams{
		ID: id, GroupID: groupID, Body: p.Body, EmailBody: p.EmailBody, Now: p.Now, Cutoff: p.Cutoff,
	})
	if err != nil || n == 0 {
		return err
	}
	return r.enqueuer.EnqueueTx(ctx, tx, jobsModel.NotifyArgs{JournalDeliveryID: &id})
}

func (r *Repository) GetDelivery(ctx context.Context, id uuid.UUID) (errorJournalUseCase.Delivery, error) {
	row, err := r.q.GetErrorNotification(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errorJournalUseCase.Delivery{}, errorJournalUseCase.ErrDeliveryGone
	}
	return deliveryOf(row), err
}
func (r *Repository) StartDelivery(ctx context.Context, id uuid.UUID, now time.Time) error {
	return r.q.StartErrorNotification(ctx, postgres.StartErrorNotificationParams{ID: id, LastAttemptAt: pgtype.Timestamptz{Time: now, Valid: true}})
}
func (r *Repository) FinishDelivery(ctx context.Context, d errorJournalUseCase.Delivery, result errorJournalUseCase.TestResult, now, next time.Time) error {
	delivered := pgtype.Timestamptz{}
	if result.OK {
		delivered = pgtype.Timestamptz{Time: now, Valid: true}
	}
	_, err := r.q.FinishErrorNotification(ctx, postgres.FinishErrorNotificationParams{ID: d.ID, LastError: result.Error, NextAttemptAt: next, DeliveredAt: delivered, CancelledAt: nullableTime(d.CancelledAt)})
	return err
}
func (r *Repository) ExpandDelivery(ctx context.Context, d errorJournalUseCase.Delivery, targets []errorJournalUseCase.DeliveryTarget, now time.Time) error {
	pool := r.pool.(interface {
		Begin(context.Context) (pgx.Tx, error)
	})
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.WithoutCancel(ctx))
	q := postgres.New(tx)
	root, err := q.LockErrorNotification(ctx, d.ID)
	if err != nil {
		return err
	}
	if root.DeliveredAt.Valid {
		return nil
	}
	for _, target := range targets {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		body := root.Body
		if target.Channel == errorJournalUseCase.ChannelEmail {
			body = root.EmailBody
		}
		n, err := q.AddErrorNotificationTarget(ctx, postgres.AddErrorNotificationTargetParams{ID: id, GroupID: root.GroupID, BatchID: uuid.NullUUID{UUID: root.ID, Valid: true}, Channel: target.Channel, Target: target.Target, Label: target.Label, Body: body, CreatedAt: now})
		if err != nil {
			return err
		}
		if n > 0 {
			if err := r.enqueuer.EnqueueTx(ctx, tx, jobsModel.NotifyArgs{JournalDeliveryID: &id}); err != nil {
				return err
			}
		}
	}
	if _, err := q.FinishErrorNotification(ctx, postgres.FinishErrorNotificationParams{ID: d.ID, NextAttemptAt: now, DeliveredAt: pgtype.Timestamptz{Time: now, Valid: true}}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (r *Repository) ListDeliveries(ctx context.Context, groupID uuid.UUID) ([]errorJournalUseCase.Delivery, error) {
	rows, err := r.q.ListErrorNotificationDeliveries(ctx, groupID)
	if err != nil {
		return nil, err
	}
	out := make([]errorJournalUseCase.Delivery, 0, len(rows))
	for _, row := range rows {
		out = append(out, deliveryOf(row))
	}
	return out, nil
}
func deliveryOf(row postgres.ErrorNotificationDelivery) errorJournalUseCase.Delivery {
	return errorJournalUseCase.Delivery{ID: row.ID, GroupID: row.GroupID, Channel: row.Channel, Target: row.Target, Label: row.Label, Body: row.Body, EmailBody: row.EmailBody, Attempts: int(row.Attempts), LastError: row.LastError, CreatedAt: row.CreatedAt, NextAttemptAt: row.NextAttemptAt, LastAttemptAt: timePtr(row.LastAttemptAt), DeliveredAt: timePtr(row.DeliveredAt), CancelledAt: timePtr(row.CancelledAt)}
}

func nullableTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
