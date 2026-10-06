package stats

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// fakeRepo is a hand double for iRepository.
type fakeRepo struct {
	getDispatch    func(context.Context, uuid.UUID) (postgres.GetDispatchRow, error)
	listDispatches func(context.Context, postgres.ListDispatchesParams) ([]postgres.ListDispatchesRow, error)
	countDispatch  func(context.Context, postgres.CountDispatchesParams) (int64, error)
	listTargets    func(context.Context, uuid.UUID) ([]postgres.NotificationDispatchTarget, error)
	byStatus       func(context.Context, time.Time) ([]postgres.CountDispatchesByStatusSinceRow, error)
	byType         func(context.Context, time.Time) ([]postgres.CountDispatchesByTypeSinceRow, error)
	byChannel      func(context.Context, time.Time) ([]postgres.CountTargetsByChannelStatusSinceRow, error)
}

func (f *fakeRepo) GetDispatch(ctx context.Context, id uuid.UUID) (postgres.GetDispatchRow, error) {
	return f.getDispatch(ctx, id)
}
func (f *fakeRepo) ListDispatches(ctx context.Context, a postgres.ListDispatchesParams) ([]postgres.ListDispatchesRow, error) {
	return f.listDispatches(ctx, a)
}
func (f *fakeRepo) CountDispatches(ctx context.Context, a postgres.CountDispatchesParams) (int64, error) {
	return f.countDispatch(ctx, a)
}
func (f *fakeRepo) ListDispatchTargets(ctx context.Context, id uuid.UUID) ([]postgres.NotificationDispatchTarget, error) {
	return f.listTargets(ctx, id)
}
func (f *fakeRepo) ListDispatchTargetsByDispatches(ctx context.Context, ids []uuid.UUID) ([]postgres.NotificationDispatchTarget, error) {
	return nil, nil
}
func (f *fakeRepo) CountDispatchesByStatusSince(ctx context.Context, t time.Time) ([]postgres.CountDispatchesByStatusSinceRow, error) {
	return f.byStatus(ctx, t)
}
func (f *fakeRepo) CountDispatchesByTypeSince(ctx context.Context, t time.Time) ([]postgres.CountDispatchesByTypeSinceRow, error) {
	return f.byType(ctx, t)
}
func (f *fakeRepo) CountTargetsByChannelStatusSince(ctx context.Context, t time.Time) ([]postgres.CountTargetsByChannelStatusSinceRow, error) {
	return f.byChannel(ctx, t)
}

func (f *fakeRepo) CountEmailDeliveredSince(context.Context, postgres.CountEmailDeliveredSinceParams) (int64, error) {
	return 0, nil
}

// Write-side methods of dispatchRepo.Queries — never reached by the stats
// use case; stubbed to satisfy the composed interface.
func (f *fakeRepo) CreateDispatch(context.Context, postgres.CreateDispatchParams) (postgres.NotificationDispatch, error) {
	panic("stats use case must not create dispatches")
}
func (f *fakeRepo) SetDispatchStatus(context.Context, postgres.SetDispatchStatusParams) error {
	panic("stats use case must not set dispatch status")
}
func (f *fakeRepo) UpsertDispatchTarget(context.Context, postgres.UpsertDispatchTargetParams) error {
	panic("stats use case must not upsert targets")
}
func (f *fakeRepo) GetActiveChannels(context.Context, postgres.GetActiveChannelsParams) ([]string, error) {
	panic("stats use case must not resolve channels")
}

func superCtx() context.Context {
	return rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RoleSuperAdmin})
}

func TestListDispatches_MapsRows(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	uc := NewNotificationStatsUseCase(Dependencies{Repo: &fakeRepo{
		listDispatches: func(_ context.Context, _ postgres.ListDispatchesParams) ([]postgres.ListDispatchesRow, error) {
			return []postgres.ListDispatchesRow{{ID: id, NotificationType: "password_reset", Status: "done"}}, nil
		},
		countDispatch: func(_ context.Context, _ postgres.CountDispatchesParams) (int64, error) { return 1, nil },
	}})
	rows, total, err := uc.ListDispatches(superCtx(), dispatchFilterZero())
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	if total != 1 || len(rows) != 1 || rows[0].ID != id || rows[0].NotificationType != "password_reset" {
		t.Fatalf("unexpected: total=%d rows=%+v", total, rows)
	}
}

func TestGetDispatch_NotFound(t *testing.T) {
	uc := NewNotificationStatsUseCase(Dependencies{Repo: &fakeRepo{
		getDispatch: func(_ context.Context, _ uuid.UUID) (postgres.GetDispatchRow, error) {
			return postgres.GetDispatchRow{}, pgx.ErrNoRows
		},
	}})
	if _, err := uc.GetDispatch(superCtx(), uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("want not-found error, got nil")
	}
}

func TestGetDispatch_WithTargets(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	uc := NewNotificationStatsUseCase(Dependencies{Repo: &fakeRepo{
		getDispatch: func(_ context.Context, _ uuid.UUID) (postgres.GetDispatchRow, error) {
			return postgres.GetDispatchRow{ID: id, NotificationType: "flag_accepted", Status: "done"}, nil
		},
		listTargets: func(_ context.Context, _ uuid.UUID) ([]postgres.NotificationDispatchTarget, error) {
			return []postgres.NotificationDispatchTarget{{DispatchID: id, Channel: "email", Status: "error", Error: "smtp 550", Attempts: 2}}, nil
		},
	}})
	d, err := uc.GetDispatch(superCtx(), id)
	if err != nil {
		t.Fatalf("GetDispatch: %v", err)
	}
	if d.ID != id || len(d.Targets) != 1 || d.Targets[0].Channel != "email" || d.Targets[0].Error != "smtp 550" || d.Targets[0].Attempts != 2 {
		t.Fatalf("unexpected detail: %+v", d)
	}
}

func TestGetStats_Aggregates(t *testing.T) {
	uc := NewNotificationStatsUseCase(Dependencies{Repo: &fakeRepo{
		byStatus: func(_ context.Context, _ time.Time) ([]postgres.CountDispatchesByStatusSinceRow, error) {
			return []postgres.CountDispatchesByStatusSinceRow{{Status: "done", Count: 7}, {Status: "pending", Count: 3}}, nil
		},
		byType: func(_ context.Context, _ time.Time) ([]postgres.CountDispatchesByTypeSinceRow, error) {
			return []postgres.CountDispatchesByTypeSinceRow{{NotificationType: "password_reset", Count: 10}}, nil
		},
		byChannel: func(_ context.Context, _ time.Time) ([]postgres.CountTargetsByChannelStatusSinceRow, error) {
			return []postgres.CountTargetsByChannelStatusSinceRow{{Channel: "email", Status: "done", Count: 6}, {Channel: "email", Status: "error", Count: 1}}, nil
		},
	}})
	s, err := uc.GetStats(superCtx(), time.Unix(0, 0))
	if err != nil {
		t.Fatalf("GetStats: %v", err)
	}
	if s.Total != 10 || len(s.ByStatus) != 2 || len(s.ByType) != 1 || len(s.ByChannel) != 2 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

// dispatchFilterZero is a tiny helper to keep tests terse.
func dispatchFilterZero() dispatchModel.ListDispatchesFilter {
	return dispatchModel.ListDispatchesFilter{Limit: 50}
}

func TestListDispatches_UserFilter_PassesThrough(t *testing.T) {
	const wantUser = "018f4b3e-1234-7abc-8def-000000000001"
	var gotListUser, gotCountUser string

	uc := NewNotificationStatsUseCase(Dependencies{Repo: &fakeRepo{
		listDispatches: func(_ context.Context, a postgres.ListDispatchesParams) ([]postgres.ListDispatchesRow, error) {
			gotListUser = a.UserFilter
			return []postgres.ListDispatchesRow{}, nil
		},
		countDispatch: func(_ context.Context, a postgres.CountDispatchesParams) (int64, error) {
			gotCountUser = a.UserFilter
			return 0, nil
		},
	}})

	_, _, err := uc.ListDispatches(superCtx(), dispatchModel.ListDispatchesFilter{User: wantUser, Limit: 50})
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	if gotListUser != wantUser {
		t.Errorf("ListDispatches params UserFilter = %q; want %q", gotListUser, wantUser)
	}
	if gotCountUser != wantUser {
		t.Errorf("CountDispatches params UserFilter = %q; want %q", gotCountUser, wantUser)
	}
}

func TestListDispatches_UserFilter_EmptyPassesEmpty(t *testing.T) {
	var gotListUser, gotCountUser string

	uc := NewNotificationStatsUseCase(Dependencies{Repo: &fakeRepo{
		listDispatches: func(_ context.Context, a postgres.ListDispatchesParams) ([]postgres.ListDispatchesRow, error) {
			gotListUser = a.UserFilter
			return []postgres.ListDispatchesRow{}, nil
		},
		countDispatch: func(_ context.Context, a postgres.CountDispatchesParams) (int64, error) {
			gotCountUser = a.UserFilter
			return 0, nil
		},
	}})

	_, _, err := uc.ListDispatches(superCtx(), dispatchFilterZero())
	if err != nil {
		t.Fatalf("ListDispatches: %v", err)
	}
	if gotListUser != "" {
		t.Errorf("ListDispatches params UserFilter = %q; want empty string", gotListUser)
	}
	if gotCountUser != "" {
		t.Errorf("CountDispatches params UserFilter = %q; want empty string", gotCountUser)
	}
}
