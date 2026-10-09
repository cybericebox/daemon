// Package eventManagerRepo maps event-local management membership between the
// domain and generated PostgreSQL rows.
package eventManagerRepo

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
)

type Queries interface {
	CreateEventManager(ctx context.Context, arg postgres.CreateEventManagerParams) (postgres.EventManager, error)
	GetEventManager(ctx context.Context, arg postgres.GetEventManagerParams) (postgres.EventManager, error)
	ListEventManagers(ctx context.Context, eventID uuid.UUID) ([]postgres.EventManager, error)
	UpsertEventManager(ctx context.Context, arg postgres.UpsertEventManagerParams) (postgres.EventManager, error)
	DeleteNonOwnerEventManager(ctx context.Context, arg postgres.DeleteNonOwnerEventManagerParams) (int64, error)
}

func (r *Repository) List(ctx context.Context, eventID uuid.UUID) ([]eventManagerModel.EventManager, error) {
	rows, err := r.q.ListEventManagers(ctx, eventID)
	if err != nil {
		return nil, err
	}
	items := make([]eventManagerModel.EventManager, 0, len(rows))
	for _, row := range rows {
		items = append(items, ToDomain(row))
	}
	return items, nil
}

func (r *Repository) Upsert(ctx context.Context, membership eventManagerModel.EventManager) (eventManagerModel.EventManager, error) {
	row, err := r.q.UpsertEventManager(ctx, postgres.UpsertEventManagerParams{
		EventID: membership.EventID, UserID: membership.UserID, Role: int16(membership.Role), CreatedAt: membership.CreatedAt,
	})
	if err != nil {
		return eventManagerModel.EventManager{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) DeleteNonOwner(ctx context.Context, eventID, userID uuid.UUID) (int64, error) {
	return r.q.DeleteNonOwnerEventManager(ctx, postgres.DeleteNonOwnerEventManagerParams{EventID: eventID, UserID: userID})
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

func (r *Repository) Create(ctx context.Context, membership eventManagerModel.EventManager) (eventManagerModel.EventManager, error) {
	row, err := r.q.CreateEventManager(ctx, postgres.CreateEventManagerParams{
		EventID:   membership.EventID,
		UserID:    membership.UserID,
		Role:      int16(membership.Role),
		CreatedAt: membership.CreatedAt,
	})
	if err != nil {
		return eventManagerModel.EventManager{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Get(ctx context.Context, eventID, userID uuid.UUID) (eventManagerModel.EventManager, error) {
	row, err := r.q.GetEventManager(ctx, postgres.GetEventManagerParams{EventID: eventID, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return eventManagerModel.EventManager{}, eventManagerModel.ErrEventManagerNotFound.Err()
	}
	if err != nil {
		return eventManagerModel.EventManager{}, err
	}
	return ToDomain(row), nil
}

func ToDomain(row postgres.EventManager) eventManagerModel.EventManager {
	return eventManagerModel.EventManager{
		EventID:   row.EventID,
		UserID:    row.UserID,
		Role:      eventManagerModel.Role(row.Role),
		CreatedAt: row.CreatedAt,
	}
}
