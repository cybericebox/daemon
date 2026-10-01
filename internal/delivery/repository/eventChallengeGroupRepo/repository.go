// Package eventChallengeGroupRepo maps event-local board sections to storage.
package eventChallengeGroupRepo

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventChallengeGroupModel "github.com/cybericebox/daemon/internal/model/eventChallengeGroup"
)

type Queries interface {
	CreateEventChallengeGroup(ctx context.Context, arg postgres.CreateEventChallengeGroupParams) (postgres.EventChallengeGroup, error)
	ListEventChallengeGroups(ctx context.Context, eventID uuid.UUID) ([]postgres.EventChallengeGroup, error)
	UpdateEventChallengeGroup(ctx context.Context, arg postgres.UpdateEventChallengeGroupParams) (postgres.EventChallengeGroup, error)
	DeleteEventChallengeGroup(ctx context.Context, arg postgres.DeleteEventChallengeGroupParams) (int64, error)
	VacateEventChallengeGroupOrders(ctx context.Context, eventID uuid.UUID) error
	SetEventChallengeGroupOrder(ctx context.Context, arg postgres.SetEventChallengeGroupOrderParams) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Create(ctx context.Context, value eventChallengeGroupModel.Group) (eventChallengeGroupModel.Group, error) {
	row, err := r.q.CreateEventChallengeGroup(ctx, postgres.CreateEventChallengeGroupParams{ID: value.ID, EventID: value.EventID, Name: value.Name, OrderIndex: value.Order, CreatedAt: value.CreatedAt})
	if err != nil {
		return eventChallengeGroupModel.Group{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) List(ctx context.Context, eventID uuid.UUID) ([]eventChallengeGroupModel.Group, error) {
	rows, err := r.q.ListEventChallengeGroups(ctx, eventID)
	if err != nil {
		return nil, err
	}
	items := make([]eventChallengeGroupModel.Group, 0, len(rows))
	for _, row := range rows {
		items = append(items, ToDomain(row))
	}
	return items, nil
}

func (r *Repository) Update(ctx context.Context, value eventChallengeGroupModel.Group) (eventChallengeGroupModel.Group, error) {
	row, err := r.q.UpdateEventChallengeGroup(ctx, postgres.UpdateEventChallengeGroupParams{ID: value.ID, EventID: value.EventID, Name: value.Name, OrderIndex: value.Order})
	if err != nil {
		return eventChallengeGroupModel.Group{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Delete(ctx context.Context, eventID, id uuid.UUID) (int64, error) {
	return r.q.DeleteEventChallengeGroup(ctx, postgres.DeleteEventChallengeGroupParams{ID: id, EventID: eventID})
}

// VacateOrders moves every group of the event to a disjoint negative order
// range so a following SetOrder sequence cannot collide with UNIQUE
// (event_id, order_index) midway. Call it inside the reorder transaction.
func (r *Repository) VacateOrders(ctx context.Context, eventID uuid.UUID) error {
	return r.q.VacateEventChallengeGroupOrders(ctx, eventID)
}

func (r *Repository) SetOrder(ctx context.Context, eventID, id uuid.UUID, order int32) (int64, error) {
	return r.q.SetEventChallengeGroupOrder(ctx, postgres.SetEventChallengeGroupOrderParams{ID: id, EventID: eventID, OrderIndex: order})
}

func ToDomain(row postgres.EventChallengeGroup) eventChallengeGroupModel.Group {
	return eventChallengeGroupModel.Group{ID: row.ID, EventID: row.EventID, Name: row.Name, Order: row.OrderIndex, CreatedAt: row.CreatedAt}
}
