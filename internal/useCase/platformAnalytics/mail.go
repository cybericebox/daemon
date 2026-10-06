package platformAnalytics

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

const (
	// mailErrorsTop is how many failure reasons the report holds.
	mailErrorsTop = 10
	// ChannelEmail and ChannelInApp are the values of the channel filter.
	ChannelEmail = "email"
	ChannelInApp = "in_app"
	// mailFilterMax bounds a filter value (it lands in the cache key).
	mailFilterMax = 64
)

// MailStore is the read port of the mail section.
type MailStore interface {
	MailSummary(ctx context.Context, from, to time.Time, f platformAnalyticsRepo.MailFilter) (platformAnalyticsRepo.MailSummary, error)
	MailErrors(ctx context.Context, from, to time.Time, f platformAnalyticsRepo.MailFilter, limit int32) ([]platformAnalyticsRepo.MailError, error)
	MailOptions(ctx context.Context, from, to time.Time, channel string, includeTests bool) (platformAnalyticsRepo.MailOptions, error)
	MailFunnels(ctx context.Context, from, to time.Time) (dispatchModel.MailFunnels, error)
}

// GetMail builds the delivery-health report of the notification channels: sent
// and failed deliveries per day, per channel, per transport (email) and per
// notification type, the failure rate and the top email failure reasons.
// channel (email | in_app; anything else reads as all), transport (email only)
// and notificationType narrow it (empty: all); SMTP test sends count only with
// includeTests.
func (u *PlatformAnalyticsUseCase) GetMail(ctx context.Context, from, to *time.Time, channel, transport, notificationType string, includeTests bool) (MailView, error) {
	if channel != ChannelEmail && channel != ChannelInApp {
		channel = ""
	}
	period, err := u.period(from, to)
	if err != nil {
		return MailView{}, err
	}
	filter := platformAnalyticsRepo.MailFilter{
		Channel:      channel,
		Transport:    clip(strings.TrimSpace(transport), mailFilterMax),
		Type:         clip(strings.TrimSpace(notificationType), mailFilterMax),
		IncludeTests: includeTests,
	}
	key := "mail|" + period.Key() + "|" + filter.Channel + "|" + filter.Transport + "|" + filter.Type + "|" + strconv.FormatBool(includeTests)
	return cached(ctx, u, key, func(ctx context.Context) (MailView, error) {
		summary, err := u.store.MailSummary(ctx, period.From, period.To, filter)
		if err != nil {
			return MailView{}, fail(err, "Failed to read mail delivery counts")
		}
		// SMTP failure reasons exist for the email channel only.
		var errs []platformAnalyticsRepo.MailError
		if filter.Channel != ChannelInApp {
			if errs, err = u.store.MailErrors(ctx, period.From, period.To, filter, mailErrorsTop); err != nil {
				return MailView{}, fail(err, "Failed to read mail errors")
			}
		}
		options, err := u.store.MailOptions(ctx, period.From, period.To, filter.Channel, includeTests)
		if err != nil {
			return MailView{}, fail(err, "Failed to read mail filter options")
		}
		funnels, err := u.store.MailFunnels(ctx, period.From, period.To)
		if err != nil {
			return MailView{}, fail(err, "Failed to read mail funnels")
		}
		view := buildMailView(period, filter, summary, errs, options)
		view.Funnels = funnels.Summary()
		return view, nil
	})
}

func buildMailView(period platformAnalyticsModel.Period, filter platformAnalyticsRepo.MailFilter, s platformAnalyticsRepo.MailSummary, errs []platformAnalyticsRepo.MailError, options platformAnalyticsRepo.MailOptions) MailView {
	view := MailView{
		Period: period, Channel: filter.Channel, Transport: filter.Transport, Type: filter.Type, IncludeTests: filter.IncludeTests,
		Daily:   denseMailDays(s.Days, period),
		Errors:  make([]MailErrorView, 0, len(errs)),
		Options: MailOptionsView{Transports: withSelected(options.Transports, filter.Transport), Types: withSelected(options.Types, filter.Type)},
	}
	for _, d := range view.Daily {
		view.Sent += d.Sent
		view.Failed += d.Failed
		view.Fallbacks += d.Fallbacks
		view.Deferred += d.Deferred
	}
	view.Total = view.Sent + view.Failed
	view.FailureRate = rate(view.Failed, view.Total)
	view.ByTransport = keyRows(s.Transports)
	view.ByChannel = keyRows(s.Channels)
	view.ByType = keyRows(s.Types)
	for _, e := range errs {
		view.Errors = append(view.Errors, MailErrorView{Code: e.Code, Message: e.Message, Total: e.Total, LastAt: e.LastAt})
	}
	return view
}

// denseMailDays lays the stored days over every UTC day of the period, so the
// chart has a point per day.
func denseMailDays(days []platformAnalyticsRepo.MailDay, period platformAnalyticsModel.Period) []MailDayView {
	byDay := make(map[int64]platformAnalyticsRepo.MailDay, len(days))
	for _, d := range days {
		byDay[d.Day.Unix()] = d
	}
	out := make([]MailDayView, 0, period.Days())
	for at := period.From; at.Before(period.To); at = at.Add(platformAnalyticsModel.Day) {
		d := byDay[at.Unix()]
		out = append(out, MailDayView{Day: at, Sent: d.Sent, Failed: d.Failed, Deferred: d.Deferred, Fallbacks: d.Fallbacks})
	}
	return out
}

func keyRows(rows []platformAnalyticsRepo.MailKey) []MailKeyView {
	out := make([]MailKeyView, 0, len(rows))
	for _, r := range rows {
		total := r.Sent + r.Failed
		out = append(out, MailKeyView{Key: r.Key, Sent: r.Sent, Failed: r.Failed, Deferred: r.Deferred, Fallbacks: r.Fallbacks, Total: total, FailureRate: rate(r.Failed, total)})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func rate(part, total int64) float64 {
	if total == 0 {
		return 0
	}
	return float64(part) / float64(total)
}

// withSelected keeps a selected filter value in its option list even when the
// period has no sends of it, so the select never shows a blank.
func withSelected(values []string, selected string) []string {
	if values == nil {
		values = []string{}
	}
	if selected == "" {
		return values
	}
	for _, v := range values {
		if v == selected {
			return values
		}
	}
	values = append(values, selected)
	sort.Strings(values)
	return values
}

func clip(value string, limit int) string {
	if r := []rune(value); len(r) > limit {
		return string(r[:limit])
	}
	return value
}
