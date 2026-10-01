package platformAnalytics

import (
	"context"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// UsersPeopleLimit bounds the per-user table of the most active accounts.
const UsersPeopleLimit = 100

// UsersStore is the read port of the users section. The role, blocked, new
// and active counts are the same statements the admin user stats and the
// overview use.
type UsersStore interface {
	UsersByRole(ctx context.Context) ([]platformAnalyticsRepo.UsersRoleCount, error)
	UsersBlocked(ctx context.Context, status string) (int64, error)
	OverviewUsers(ctx context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewUsers, error)
	ActiveUsers(ctx context.Context, w platformAnalyticsRepo.OverviewWindow) (platformAnalyticsRepo.OverviewActiveUsers, error)
	OverviewUsersDays(ctx context.Context, from, to time.Time) ([]platformAnalyticsRepo.OverviewUsersDay, error)
	UsersActiveDays(ctx context.Context, from, to time.Time) ([]platformAnalyticsRepo.UsersActiveDay, error)
	UsersMethods(ctx context.Context, from, to time.Time) (platformAnalyticsRepo.UsersMethods, error)
	UsersRetention(ctx context.Context, to time.Time) (platformAnalyticsRepo.UsersRetention, error)
	UsersPeople(ctx context.Context, limit int32) ([]platformAnalyticsRepo.UsersPerson, error)
}

// GetUsers is the «Користувачі» report: the aggregates of the admin user
// stats (total, blocked, by role, new and active accounts, registrations per
// day) bound to the chosen period, plus daily and weekly active accounts,
// the sign-in method split and how many events accounts joined. Aggregates
// only: per-user rows are GetUsersPeople.
func (u *PlatformAnalyticsUseCase) GetUsers(ctx context.Context, from, to *time.Time) (UsersView, error) {
	period, err := u.period(from, to)
	if err != nil {
		return UsersView{}, err
	}
	return cached(ctx, u, "users:"+period.Key(), func(ctx context.Context) (UsersView, error) {
		return u.loadUsers(ctx, period)
	})
}

func (u *PlatformAnalyticsUseCase) loadUsers(ctx context.Context, p platformAnalyticsModel.Period) (UsersView, error) {
	w := overviewPeriodWindow(p)
	var (
		byRole        []platformAnalyticsRepo.UsersRoleCount
		blocked       int64
		users         platformAnalyticsRepo.OverviewUsers
		active        platformAnalyticsRepo.OverviewActiveUsers
		registrations []platformAnalyticsRepo.OverviewUsersDay
		activeDays    []platformAnalyticsRepo.UsersActiveDay
		methods       platformAnalyticsRepo.UsersMethods
		retention     platformAnalyticsRepo.UsersRetention
	)
	g, gctx := errgroup.WithContext(ctx)
	run := func(message string, load func(context.Context) error) {
		g.Go(func() error {
			if err := load(gctx); err != nil {
				return fail(err, message)
			}
			return nil
		})
	}
	run("Failed to count users by role", func(c context.Context) (err error) { byRole, err = u.store.UsersByRole(c); return })
	run("Failed to count blocked users", func(c context.Context) (err error) {
		blocked, err = u.store.UsersBlocked(c, string(userModel.UserStatusBlocked))
		return
	})
	run("Failed to count new users", func(c context.Context) (err error) { users, err = u.store.OverviewUsers(c, w); return })
	run("Failed to count active users", func(c context.Context) (err error) { active, err = u.store.ActiveUsers(c, w); return })
	run("Failed to load registrations by day", func(c context.Context) (err error) {
		registrations, err = u.store.OverviewUsersDays(c, p.From, p.To)
		return
	})
	run("Failed to load active users by day", func(c context.Context) (err error) {
		activeDays, err = u.store.UsersActiveDays(c, p.From, p.To)
		return
	})
	run("Failed to count sign-in methods", func(c context.Context) (err error) { methods, err = u.store.UsersMethods(c, p.From, p.To); return })
	run("Failed to count event participation", func(c context.Context) (err error) { retention, err = u.store.UsersRetention(c, p.To); return })
	if err := g.Wait(); err != nil {
		return UsersView{}, err
	}

	view := UsersView{
		Period:  overviewPeriodViewOf(p),
		Total:   users.Total,
		Blocked: blocked,
		New:     overviewMetricOf(p, users.New, users.NewPrev),
		Active:  overviewMetricOf(p, active.Active, active.ActivePrev),
		ByRole:  make([]UsersRoleView, 0, len(byRole)),
		Methods: []UsersMethodView{
			{Method: UsersMethodPassword, Total: methods.PasswordOnly, New: methods.PasswordOnlyNew},
			{Method: UsersMethodGoogle, Total: methods.ProviderOnly, New: methods.ProviderOnlyNew},
			{Method: UsersMethodBoth, Total: methods.Both, New: methods.BothNew},
			{Method: UsersMethodNone, Total: methods.None, New: methods.NoneNew},
		},
		Retention: UsersRetentionView{One: retention.One, Two: retention.Two, ThreePlus: retention.ThreePlus, Never: retention.Never},
	}
	for _, r := range byRole {
		view.ByRole = append(view.ByRole, UsersRoleView{Role: r.Role, Count: r.Count})
	}
	view.Registrations = make([]OverviewDayNewView, 0, len(registrations))
	for _, d := range overviewTrimLeading(p, registrations, func(d platformAnalyticsRepo.OverviewUsersDay) bool { return d.New == 0 }) {
		view.Registrations = append(view.Registrations, OverviewDayNewView{Day: d.Day, New: d.New})
	}
	view.ActiveByDay = make([]UsersActiveDayView, 0, len(activeDays))
	var dauSum int64
	for _, d := range activeDays {
		dauSum += d.DAU
	}
	if len(activeDays) > 0 {
		view.AvgDailyActive = float64(dauSum) / float64(len(activeDays))
	}
	for _, d := range overviewTrimLeading(p, activeDays, func(d platformAnalyticsRepo.UsersActiveDay) bool { return d.DAU == 0 && d.WAU == 0 }) {
		view.ActiveByDay = append(view.ActiveByDay, UsersActiveDayView{Day: d.Day, DAU: d.DAU, WAU: d.WAU})
	}
	return view, nil
}

// GetUsersPeople is the per-user table of the most active accounts (events
// joined, solves), at most UsersPeopleLimit rows. It holds personal data:
// the caller must hold analytics.users.read.
func (u *PlatformAnalyticsUseCase) GetUsersPeople(ctx context.Context) ([]UsersPersonView, error) {
	return cached(ctx, u, "users-people", func(ctx context.Context) ([]UsersPersonView, error) {
		rows, err := u.store.UsersPeople(ctx, UsersPeopleLimit)
		if err != nil {
			return nil, fail(err, "Failed to read the most active users")
		}
		out := make([]UsersPersonView, 0, len(rows))
		for _, r := range rows {
			out = append(out, UsersPersonView{
				ID: r.ID, Name: strings.TrimSpace(r.FirstName + " " + r.LastName), Email: r.Email, Role: r.Role,
				EventsJoined: r.EventsJoined, Solves: r.Solves, LastSeenAt: r.LastSeenAt,
			})
		}
		return out, nil
	})
}
