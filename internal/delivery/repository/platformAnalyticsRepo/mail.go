package platformAnalyticsRepo

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

// MailQueries are the sqlc statements of the mail section.
type MailQueries interface {
	ListPlatformMailSummary(context.Context, postgres.ListPlatformMailSummaryParams) ([]postgres.ListPlatformMailSummaryRow, error)
	ListPlatformMailErrors(context.Context, postgres.ListPlatformMailErrorsParams) ([]postgres.ListPlatformMailErrorsRow, error)
	ListPlatformMailOptions(context.Context, postgres.ListPlatformMailOptionsParams) ([]postgres.ListPlatformMailOptionsRow, error)
	GetMailFunnels(context.Context, postgres.GetMailFunnelsParams) (postgres.GetMailFunnelsRow, error)
	GetPlatformAccountFunnel(context.Context, postgres.GetPlatformAccountFunnelParams) (postgres.GetPlatformAccountFunnelRow, error)
}

type (
	// MailFilter narrows the report: empty Channel / Transport / Type mean all.
	// Channel is email or in_app; Transport applies to email targets only.
	MailFilter struct {
		Channel      string
		Transport    string
		Type         string
		IncludeTests bool
	}

	// MailCount is one sent / failed / deferred tally.
	MailCount struct {
		Sent      int64
		Failed    int64
		Deferred  int64
		Fallbacks int64
	}

	// MailDay is the tally of one UTC day.
	MailDay struct {
		Day time.Time
		MailCount
	}

	// MailKey is the tally of one transport, channel or notification type.
	MailKey struct {
		Key string
		MailCount
	}

	// MailSummary holds the groupings of the delivery counts.
	MailSummary struct {
		Days       []MailDay
		Transports []MailKey
		Channels   []MailKey
		Types      []MailKey
	}

	// MailError is one normalized failure reason (never an address).
	MailError struct {
		Code    string
		Message string
		Total   int64
		LastAt  time.Time
	}

	// MailOptions are the values the filters offer.
	MailOptions struct {
		Transports []string
		Types      []string
	}
)

// MailSummary reads the per-day, per-transport, per-channel and per-type counts of
// [from, to) in one statement.
func (r *Repository) MailSummary(ctx context.Context, from, to time.Time, f MailFilter) (MailSummary, error) {
	rows, err := r.q.ListPlatformMailSummary(ctx, postgres.ListPlatformMailSummaryParams{
		FromAt: from, ToAt: to, IncludeTests: f.IncludeTests, Channel: f.Channel, Transport: f.Transport, NotificationType: f.Type,
	})
	if err != nil {
		return MailSummary{}, err
	}
	var out MailSummary
	for _, row := range rows {
		count := MailCount{Sent: row.Sent, Failed: row.Failed, Deferred: row.Deferred, Fallbacks: row.Fallbacks}
		switch row.Dimension {
		case "day":
			out.Days = append(out.Days, MailDay{Day: row.Day, MailCount: count})
		case "transport":
			out.Transports = append(out.Transports, MailKey{Key: row.Key, MailCount: count})
		case "channel":
			out.Channels = append(out.Channels, MailKey{Key: row.Key, MailCount: count})
		default:
			out.Types = append(out.Types, MailKey{Key: row.Key, MailCount: count})
		}
	}
	return out, nil
}

// MailErrors reads the top failure reasons.
func (r *Repository) MailErrors(ctx context.Context, from, to time.Time, f MailFilter, limit int32) ([]MailError, error) {
	rows, err := r.q.ListPlatformMailErrors(ctx, postgres.ListPlatformMailErrorsParams{
		FromAt: from, ToAt: to, IncludeTests: f.IncludeTests, Transport: f.Transport, NotificationType: f.Type, LimitVal: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]MailError, 0, len(rows))
	for _, row := range rows {
		out = append(out, MailError{Code: row.Code, Message: row.Message, Total: row.Total, LastAt: row.LastAt})
	}
	return out, nil
}

// MailOptions reads the email transports and the types seen in the period on
// the channel (empty: all).
func (r *Repository) MailOptions(ctx context.Context, from, to time.Time, channel string, includeTests bool) (MailOptions, error) {
	rows, err := r.q.ListPlatformMailOptions(ctx, postgres.ListPlatformMailOptionsParams{FromAt: from, ToAt: to, Channel: channel, IncludeTests: includeTests})
	if err != nil {
		return MailOptions{}, err
	}
	var out MailOptions
	for _, row := range rows {
		if row.Kind == "transport" {
			out.Transports = append(out.Transports, row.Value)
		} else {
			out.Types = append(out.Types, row.Value)
		}
	}
	return out, nil
}

// MailFunnels reads the invitation and application outcomes of every event in
// [from, to) and the account registration funnel of the same window.
func (r *Repository) MailFunnels(ctx context.Context, from, to time.Time) (dispatchModel.MailFunnels, error) {
	row, err := r.q.GetMailFunnels(ctx, postgres.GetMailFunnelsParams{
		FromAt: pgtype.Timestamptz{Time: from, Valid: true}, ToAt: pgtype.Timestamptz{Time: to, Valid: true},
	})
	if err != nil {
		return dispatchModel.MailFunnels{}, err
	}
	out := dispatchRepo.MailFunnelsFromRow(row)
	accounts, err := r.q.GetPlatformAccountFunnel(ctx, postgres.GetPlatformAccountFunnelParams{FromAt: from, ToAt: to})
	if err != nil {
		return dispatchModel.MailFunnels{}, err
	}
	out.RegistrationsStarted, out.RegistrationsCompleted = accounts.Started, accounts.Completed
	return out, nil
}
