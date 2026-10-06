package platformAnalytics

import (
	"time"

	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

type (
	// MailView is the delivery-health report of the notification channels. One
	// send is one target (email or in-app) of a notification dispatch.
	MailView struct {
		Period platformAnalyticsModel.Period
		// The filters the report was built with.
		Channel      string
		Transport    string
		Type         string
		IncludeTests bool

		Sent   int64
		Failed int64
		// Deferred deliveries were held back by the SMTP send limit; they are
		// not part of Total.
		Deferred int64
		Total    int64
		// FailureRate is failed / (sent + failed), 0 with no sends.
		FailureRate float64
		// Fallbacks counts sends that failed on the Event SMTP and went out
		// through the platform transport.
		Fallbacks int64

		Daily       []MailDayView
		ByTransport []MailKeyView
		ByChannel   []MailKeyView
		ByType      []MailKeyView
		Errors      []MailErrorView
		Options     MailOptionsView
		// Funnels are the invitation, registration and application outcomes of
		// the period; the transport and type filters do not apply to them.
		Funnels dispatchModel.FunnelsSummary
	}

	MailDayView struct {
		Day       time.Time
		Sent      int64
		Failed    int64
		Deferred  int64
		Fallbacks int64
	}

	// MailKeyView is the tally of one channel, transport or notification type.
	MailKeyView struct {
		Key         string
		Sent        int64
		Failed      int64
		Deferred    int64
		Fallbacks   int64
		Total       int64
		FailureRate float64
	}

	// MailErrorView is one normalized failure reason; it holds no address.
	MailErrorView struct {
		Code    string
		Message string
		Total   int64
		LastAt  time.Time
	}

	// MailOptionsView are the values the filters offer.
	MailOptionsView struct {
		Transports []string
		Types      []string
	}
)
