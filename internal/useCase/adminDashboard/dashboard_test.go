package adminDashboard_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/dashboardRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/useCase/adminDashboard"
)

type fakeStore struct{ calls []string }

func (f *fakeStore) FailedStands(context.Context, time.Time, int32) (dashboardRepo.ItemList, error) {
	f.calls = append(f.calls, "failed")
	return dashboardRepo.ItemList{Total: 3, Items: []dashboardRepo.Item{{ID: uuid.Must(uuid.NewV7()), Label: "CTF", Count: 3}}}, nil
}
func (f *fakeStore) EventsWithoutReservation(context.Context, time.Time, time.Time, int32) (dashboardRepo.ItemList, error) {
	f.calls = append(f.calls, "noreservation")
	return dashboardRepo.ItemList{}, nil
}
func (f *fakeStore) PendingElevations(context.Context) (int64, *time.Time, error) {
	f.calls = append(f.calls, "elevations")
	return 2, nil, nil
}
func (f *fakeStore) OpenErrors(context.Context, int32) (dashboardRepo.ItemList, error) {
	f.calls = append(f.calls, "errors")
	return dashboardRepo.ItemList{Total: 1, Items: []dashboardRepo.Item{{Label: "boom", Count: 9}}}, nil
}
func (f *fakeStore) MailErrors(context.Context, time.Time) (int64, error) {
	f.calls = append(f.calls, "mail")
	return 4, nil
}
func (f *fakeStore) CurrentEvents(context.Context, time.Time, time.Time, int32) ([]dashboardRepo.CurrentEvent, error) {
	f.calls = append(f.calls, "events")
	return []dashboardRepo.CurrentEvent{{Name: "CTF", Running: true, StandsReady: 2, StandsTotal: 3}}, nil
}
func (f *fakeStore) Trends(_ context.Context, from time.Time, days int32) ([]dashboardRepo.TrendDay, error) {
	f.calls = append(f.calls, "trends")
	out := make([]dashboardRepo.TrendDay, 0, days)
	for i := range days {
		out = append(out, dashboardRepo.TrendDay{Day: from.AddDate(0, 0, int(i)), Registrations: int64(i), ActiveUsers: 2, Solves: 5})
	}
	return out, nil
}

type fakeInfra struct{}

func (fakeInfra) InfraStandCounts(context.Context) (platformAnalyticsRepo.InfraStandCounts, error) {
	return platformAnalyticsRepo.InfraStandCounts{Creating: 1, Ready: 4, Failed: 2}, nil
}
func (fakeInfra) InfraCapacity(context.Context, time.Time, time.Time, float64) ([]platformAnalyticsRepo.InfraCapacity, error) {
	return []platformAnalyticsRepo.InfraCapacity{{AllocatableCPUMillis: 10000, RequestedCPUMillis: 4000, AllocatableMemory: 1 << 30, RequestedMemory: 1 << 29, Agents: 2}}, nil
}

func as(role rbac.Role) context.Context {
	return rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: uuid.Must(uuid.NewV7()), Role: role})
}

func newDashboard(store *fakeStore) *adminDashboard.Dashboard {
	d := adminDashboard.New(store, fakeInfra{})
	d.SetClock(func() time.Time { return time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC) })
	return d
}

func TestDashboardGivesASuperAdminEveryBlock(t *testing.T) {
	store := &fakeStore{}

	view, err := newDashboard(store).GetAdminDashboard(as(rbac.RoleSuperAdmin))

	require.NoError(t, err)
	kinds := map[string]adminDashboard.Attention{}
	for _, a := range view.Attention {
		kinds[a.Kind] = a
	}
	require.Contains(t, kinds, adminDashboard.KindFailedStands)
	require.EqualValues(t, 3, kinds[adminDashboard.KindFailedStands].Count)
	require.Equal(t, adminDashboard.TargetStands, kinds[adminDashboard.KindFailedStands].Target)
	require.NotContains(t, kinds, adminDashboard.KindEventsNoReservation, "an empty list is not shown")
	require.Contains(t, kinds, adminDashboard.KindPendingElevations)
	require.Contains(t, kinds, adminDashboard.KindOpenErrors)
	require.EqualValues(t, 4, kinds[adminDashboard.KindMailErrors].Count)
	require.Len(t, view.CurrentAndUpcoming, 1)
	require.NotNil(t, view.Trends7d)
	require.Len(t, view.Trends7d.Days, 7)
	require.Equal(t, time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC), view.Trends7d.Days[0], "seven days ending today")
	require.Len(t, view.Trends7d.Solves, 7)
	require.NotNil(t, view.Infrastructure)
	require.EqualValues(t, 5, view.Infrastructure.ActiveStands)
	require.EqualValues(t, 2, view.Infrastructure.FailedStands)
	require.EqualValues(t, 10000, view.Infrastructure.CPUAllocatableMillicores)
}

func TestDashboardHidesWhatTheRoleMayNotSeeAndNeverQueriesIt(t *testing.T) {
	store := &fakeStore{}

	view, err := newDashboard(store).GetAdminDashboard(as(rbac.RoleUser))

	require.NoError(t, err)
	require.Empty(t, view.Attention)
	require.Nil(t, view.CurrentAndUpcoming)
	require.Nil(t, view.Trends7d)
	require.Nil(t, view.Infrastructure)
	require.Empty(t, store.calls, "a block without the permission costs no query")

	// the permissions are separate: a series is shown only to the role that may read its source
	for _, role := range []rbac.Role{rbac.RoleAdmin, rbac.RoleAdminViewer} {
		view, err = newDashboard(&fakeStore{}).GetAdminDashboard(as(role))
		require.NoError(t, err, role)
		if role.HasPermission(rbac.PermAnalyticsRead) {
			require.NotNil(t, view.Trends7d, role)
		}
		if view.Trends7d != nil && !role.HasPermission(rbac.PermAnalyticsRead) {
			require.Nil(t, view.Trends7d.Solves, role)
		}
	}
}
