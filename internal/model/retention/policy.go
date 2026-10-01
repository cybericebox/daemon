// Package retentionModel holds the data retention periods of the Privacy
// Policy ("Retention") and the cutoffs they give at a moment in time.
package retentionModel

import (
	"errors"
	"time"

	"github.com/gofrs/uuid"
)

const day = 24 * time.Hour

// Policy defaults, equal to the published Privacy Policy.
const (
	DefaultSessionAfterExpiry       = 90 * day
	DefaultLabTelemetry             = 90 * day
	DefaultDeliveryLog              = 180 * day
	DefaultFormAnswersAfterEventEnd = 365 * day
	DefaultInactiveAccount          = 3 * 365 * day
	DefaultInactivityGrace          = 30 * day
	DefaultExpiredInvitation        = 7 * day
	DefaultPendingAccount           = 30 * day
	DefaultUnusedAnswerFile         = day
	DefaultSignalHistory            = 365 * day
	DefaultEventAnalyticsAfterEnd   = 365 * day
	DefaultBatchSize                = 1000
)

type (
	// Policy is how long each kind of data is kept.
	Policy struct {
		// SessionAfterExpiry keeps an expired session record (IP, user agent).
		SessionAfterExpiry time.Duration
		// LabTelemetry keeps lab observations (event and platform capacity).
		LabTelemetry time.Duration
		// DeliveryLog keeps the email and notification delivery journal.
		DeliveryLog time.Duration
		// FormAnswersAfterEventEnd keeps registration form answers after the
		// event's effective finish.
		FormAnswersAfterEventEnd time.Duration
		// InactiveAccount is the inactivity after which the owner is warned.
		InactiveAccount time.Duration
		// InactivityGrace is the time between the warning and the deletion.
		InactivityGrace time.Duration
		// ExpiredInvitation keeps an event invitation that can no longer be
		// accepted (event finished, withdrawn, or started with a closed
		// roster), so a quickly rescheduled event does not lose it.
		ExpiredInvitation time.Duration
		// PendingAccount keeps an account whose registration was never
		// finished (an event or platform invitation, or a self sign-up that
		// never confirmed), counted from its creation; a live event
		// invitation keeps it longer.
		PendingAccount time.Duration
		// UnusedAnswerFile keeps a file uploaded for a form answer that was
		// never saved with an answer.
		UnusedAnswerFile time.Duration
		// SignalHistory keeps a finished system signal (the history of
		// registrations, invitations and other event facts).
		SignalHistory time.Duration
		// EventAnalyticsAfterEnd keeps the event analytics logs (activity,
		// stand transitions, VPN sessions) after the event's effective finish.
		EventAnalyticsAfterEnd time.Duration
		// BatchSize bounds the rows one purge statement touches.
		BatchSize int32
	}

	// Cutoffs are the instants before which data is past its period.
	Cutoffs struct {
		SessionsExpiredBefore   time.Time
		LabObservedBefore       time.Time
		DispatchesCreatedBefore time.Time
		EventsEndedBefore       time.Time
		// InactiveSince: accounts last seen before it get the warning.
		InactiveSince time.Time
		// WarnedBefore: accounts warned before it are deleted if still inactive.
		WarnedBefore time.Time
		// InvitationsExpiredBefore: invitations dead before it are removed.
		InvitationsExpiredBefore time.Time
		// PendingAccountsCreatedBefore: unconfirmed accounts created before it
		// are removed unless they hold a live invitation.
		PendingAccountsCreatedBefore time.Time
		// UnusedAnswerFilesCreatedBefore: answer uploads never used in an
		// answer and created before it are removed.
		UnusedAnswerFilesCreatedBefore time.Time
		// SignalsCreatedBefore: finished signals created before it are removed.
		SignalsCreatedBefore time.Time
		// AnalyticsEventsEndedBefore: analytics logs of events that ended
		// before it are removed.
		AnalyticsEventsEndedBefore time.Time
	}
)

// DefaultPolicy returns the published policy.
func DefaultPolicy() Policy {
	return Policy{
		SessionAfterExpiry:       DefaultSessionAfterExpiry,
		LabTelemetry:             DefaultLabTelemetry,
		DeliveryLog:              DefaultDeliveryLog,
		FormAnswersAfterEventEnd: DefaultFormAnswersAfterEventEnd,
		InactiveAccount:          DefaultInactiveAccount,
		InactivityGrace:          DefaultInactivityGrace,
		ExpiredInvitation:        DefaultExpiredInvitation,
		PendingAccount:           DefaultPendingAccount,
		UnusedAnswerFile:         DefaultUnusedAnswerFile,
		SignalHistory:            DefaultSignalHistory,
		EventAnalyticsAfterEnd:   DefaultEventAnalyticsAfterEnd,
		BatchSize:                DefaultBatchSize,
	}
}

// Validate rejects a policy that would purge everything at once.
func (p Policy) Validate() error {
	for _, period := range []time.Duration{
		p.SessionAfterExpiry, p.LabTelemetry, p.DeliveryLog,
		p.FormAnswersAfterEventEnd, p.InactiveAccount, p.InactivityGrace,
		p.ExpiredInvitation, p.PendingAccount, p.UnusedAnswerFile, p.SignalHistory,
		p.EventAnalyticsAfterEnd,
	} {
		if period < day {
			return errors.New("retention: every period must be at least 24h")
		}
	}
	if p.BatchSize < 1 {
		return errors.New("retention: batch size must be at least 1")
	}
	return nil
}

// Cutoffs computes every purge boundary at now.
func (p Policy) Cutoffs(now time.Time) Cutoffs {
	return Cutoffs{
		SessionsExpiredBefore:          now.Add(-p.SessionAfterExpiry),
		LabObservedBefore:              now.Add(-p.LabTelemetry),
		DispatchesCreatedBefore:        now.Add(-p.DeliveryLog),
		EventsEndedBefore:              now.Add(-p.FormAnswersAfterEventEnd),
		InactiveSince:                  now.Add(-p.InactiveAccount),
		WarnedBefore:                   now.Add(-p.InactivityGrace),
		InvitationsExpiredBefore:       now.Add(-p.ExpiredInvitation),
		PendingAccountsCreatedBefore:   now.Add(-p.PendingAccount),
		UnusedAnswerFilesCreatedBefore: now.Add(-p.UnusedAnswerFile),
		SignalsCreatedBefore:           now.Add(-p.SignalHistory),
		AnalyticsEventsEndedBefore:     now.Add(-p.EventAnalyticsAfterEnd),
	}
}

// DeletionDate is when an account warned at warnedAt is deleted if its owner
// does not sign in (the job runs daily, so deletion follows within a day).
func (p Policy) DeletionDate(warnedAt time.Time) time.Time {
	return warnedAt.Add(p.InactivityGrace)
}

type (
	// InactiveAccount is an account due for the inactivity warning.
	InactiveAccount struct {
		UserID    uuid.UUID
		FirstName string
		LastSeen  time.Time
	}

	// WarnedAccount is a warned account due for deletion.
	WarnedAccount struct {
		UserID   uuid.UUID
		WarnedAt time.Time
	}
)
