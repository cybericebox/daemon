package platformAnalyticsRepo

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// OverviewQueries are the sqlc statements of the overview section (the
// active-account and new-account statements are shared with «Користувачі»).
type OverviewQueries interface {
	GetPlatformOverviewUsers(context.Context, postgres.GetPlatformOverviewUsersParams) (postgres.GetPlatformOverviewUsersRow, error)
	GetPlatformActiveUsers(context.Context, postgres.GetPlatformActiveUsersParams) (postgres.GetPlatformActiveUsersRow, error)
	GetPlatformOverviewEvents(context.Context, postgres.GetPlatformOverviewEventsParams) (postgres.GetPlatformOverviewEventsRow, error)
	GetPlatformOverviewParticipants(context.Context, postgres.GetPlatformOverviewParticipantsParams) (postgres.GetPlatformOverviewParticipantsRow, error)
	GetPlatformOverviewActivity(context.Context, postgres.GetPlatformOverviewActivityParams) (postgres.GetPlatformOverviewActivityRow, error)
	GetPlatformOverviewMail(context.Context, postgres.GetPlatformOverviewMailParams) (postgres.GetPlatformOverviewMailRow, error)
	GetPlatformOverviewStands(context.Context, postgres.GetPlatformOverviewStandsParams) (postgres.GetPlatformOverviewStandsRow, error)
	ListPlatformOverviewDailyUsers(context.Context, postgres.ListPlatformOverviewDailyUsersParams) ([]postgres.ListPlatformOverviewDailyUsersRow, error)
	ListPlatformOverviewDailyActivity(context.Context, postgres.ListPlatformOverviewDailyActivityParams) ([]postgres.ListPlatformOverviewDailyActivityRow, error)
	ListPlatformOverviewDailyMail(context.Context, postgres.ListPlatformOverviewDailyMailParams) ([]postgres.ListPlatformOverviewDailyMailRow, error)
}

type (
	// OverviewWindow is the report window [From, To) and the previous one of
	// the same length [PrevFrom, PrevTo); for an all-time report the previous
	// window is empty (PrevFrom == PrevTo).
	OverviewWindow struct {
		From, To, PrevFrom, PrevTo time.Time
	}

	OverviewUsers struct{ Total, New, NewPrev int64 }

	OverviewActiveUsers struct{ Active, ActivePrev int64 }

	OverviewEvents struct {
		Draft, Published, Running, Finished, Archived, Total int64
		New, NewPrev                                         int64
	}

	OverviewParticipants struct{ Registered, RegisteredPrev, Approved, ApprovedPrev int64 }

	OverviewActivity struct{ Attempts, AttemptsPrev, Solves, SolvesPrev int64 }

	OverviewMail struct{ Sent, SentPrev, Failed, FailedPrev int64 }

	OverviewStands struct{ Ready, Creating, FailedNow, Failures, FailuresPrev int64 }

	// OverviewUsersDay is the accounts registered on one UTC day.
	OverviewUsersDay struct {
		Day time.Time
		New int64
	}

	// OverviewActivityDay is the attempts and solves of one UTC day.
	OverviewActivityDay struct {
		Day              time.Time
		Attempts, Solves int64
	}

	// OverviewMailDay is the email targets sent and failed on one UTC day.
	OverviewMailDay struct {
		Day          time.Time
		Sent, Failed int64
	}
)

func (r *Repository) OverviewUsers(ctx context.Context, w OverviewWindow) (OverviewUsers, error) {
	row, err := r.q.GetPlatformOverviewUsers(ctx, postgres.GetPlatformOverviewUsersParams{FromAt: w.From, ToAt: w.To, PrevFrom: w.PrevFrom, PrevTo: w.PrevTo})
	return OverviewUsers{Total: row.Total, New: row.NewUsers, NewPrev: row.NewPrev}, err
}

// ActiveUsers counts the accounts with recorded activity in each window.
func (r *Repository) ActiveUsers(ctx context.Context, w OverviewWindow) (OverviewActiveUsers, error) {
	row, err := r.q.GetPlatformActiveUsers(ctx, postgres.GetPlatformActiveUsersParams{FromAt: w.From, ToAt: w.To, PrevFrom: w.PrevFrom, PrevTo: w.PrevTo})
	return OverviewActiveUsers{Active: row.Active, ActivePrev: row.ActivePrev}, err
}

// OverviewEvents counts events by lifecycle status at now.
func (r *Repository) OverviewEvents(ctx context.Context, w OverviewWindow, now time.Time) (OverviewEvents, error) {
	row, err := r.q.GetPlatformOverviewEvents(ctx, postgres.GetPlatformOverviewEventsParams{FromAt: w.From, ToAt: w.To, PrevFrom: w.PrevFrom, PrevTo: w.PrevTo, Now: now})
	return OverviewEvents{
		Draft: row.Draft, Published: row.Published, Running: row.Running, Finished: row.Finished, Archived: row.Archived,
		Total: row.Total, New: row.NewEvents, NewPrev: row.NewPrev,
	}, err
}

func (r *Repository) OverviewParticipants(ctx context.Context, w OverviewWindow) (OverviewParticipants, error) {
	row, err := r.q.GetPlatformOverviewParticipants(ctx, postgres.GetPlatformOverviewParticipantsParams{FromAt: w.From, ToAt: w.To, PrevFrom: w.PrevFrom, PrevTo: w.PrevTo})
	return OverviewParticipants{Registered: row.Registered, RegisteredPrev: row.RegisteredPrev, Approved: row.Approved, ApprovedPrev: row.ApprovedPrev}, err
}

func (r *Repository) OverviewActivity(ctx context.Context, w OverviewWindow) (OverviewActivity, error) {
	row, err := r.q.GetPlatformOverviewActivity(ctx, postgres.GetPlatformOverviewActivityParams{FromAt: w.From, ToAt: w.To, PrevFrom: w.PrevFrom, PrevTo: w.PrevTo})
	return OverviewActivity{Attempts: row.Attempts, AttemptsPrev: row.AttemptsPrev, Solves: row.Solves, SolvesPrev: row.SolvesPrev}, err
}

func (r *Repository) OverviewMail(ctx context.Context, w OverviewWindow) (OverviewMail, error) {
	row, err := r.q.GetPlatformOverviewMail(ctx, postgres.GetPlatformOverviewMailParams{FromAt: w.From, ToAt: w.To, PrevFrom: w.PrevFrom, PrevTo: w.PrevTo})
	return OverviewMail{Sent: row.Sent, SentPrev: row.SentPrev, Failed: row.Failed, FailedPrev: row.FailedPrev}, err
}

func (r *Repository) OverviewStands(ctx context.Context, w OverviewWindow) (OverviewStands, error) {
	row, err := r.q.GetPlatformOverviewStands(ctx, postgres.GetPlatformOverviewStandsParams{FromAt: w.From, ToAt: w.To, PrevFrom: w.PrevFrom, PrevTo: w.PrevTo})
	return OverviewStands{Ready: row.Ready, Creating: row.Creating, FailedNow: row.FailedNow, Failures: row.Failures, FailuresPrev: row.FailuresPrev}, err
}

// OverviewUsersDays lists new accounts per UTC day over [from, to), zero-filled.
func (r *Repository) OverviewUsersDays(ctx context.Context, from, to time.Time) ([]OverviewUsersDay, error) {
	rows, err := r.q.ListPlatformOverviewDailyUsers(ctx, postgres.ListPlatformOverviewDailyUsersParams{FromAt: from, ToAt: to})
	if err != nil {
		return nil, err
	}
	out := make([]OverviewUsersDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, OverviewUsersDay{Day: row.Day, New: row.NewUsers})
	}
	return out, nil
}

// OverviewActivityDays lists attempts and solves per UTC day over [from, to), zero-filled.
func (r *Repository) OverviewActivityDays(ctx context.Context, from, to time.Time) ([]OverviewActivityDay, error) {
	rows, err := r.q.ListPlatformOverviewDailyActivity(ctx, postgres.ListPlatformOverviewDailyActivityParams{FromAt: from, ToAt: to})
	if err != nil {
		return nil, err
	}
	out := make([]OverviewActivityDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, OverviewActivityDay{Day: row.Day, Attempts: row.Attempts, Solves: row.Solves})
	}
	return out, nil
}

// OverviewMailDays lists email targets sent and failed per UTC day over [from, to), zero-filled.
func (r *Repository) OverviewMailDays(ctx context.Context, from, to time.Time) ([]OverviewMailDay, error) {
	rows, err := r.q.ListPlatformOverviewDailyMail(ctx, postgres.ListPlatformOverviewDailyMailParams{FromAt: from, ToAt: to})
	if err != nil {
		return nil, err
	}
	out := make([]OverviewMailDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, OverviewMailDay{Day: row.Day, Sent: row.Sent, Failed: row.Failed})
	}
	return out, nil
}
