// Package errorJournal is the platform error journal: what broke, grouped by fingerprint. It is separate from the
// admin audit log (who did what). Nothing here stores an IP address; messages are scrubbed of secrets, tokens and
// e-mail addresses before they are kept.
package errorJournal

import (
	"time"

	"github.com/gofrs/uuid"
)

// Kind says what produced an error.
type Kind string

const (
	KindHTTP5xx         Kind = "http_5xx"
	KindPanic           Kind = "panic"
	KindHTTP403         Kind = "http_403"
	KindHTTP429         Kind = "http_429"
	KindJob             Kind = "job"
	KindQueue           Kind = "queue"
	KindMail            Kind = "mail"
	KindLabAgentOffline Kind = "lab_agent_offline"
	KindLabDeploy       Kind = "lab_deploy"
	KindLabCertExpiry   Kind = "lab_cert_expiry"
	KindLabComponent    Kind = "lab_component"
)

// Kinds lists every kind, in the order the admin page shows them.
var Kinds = []Kind{
	KindHTTP5xx, KindPanic, KindHTTP403, KindHTTP429, KindJob, KindQueue, KindMail,
	KindLabAgentOffline, KindLabDeploy, KindLabCertExpiry, KindLabComponent,
}

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	for _, known := range Kinds {
		if k == known {
			return true
		}
	}
	return false
}

// Status is the state of a group.
type Status string

const (
	StatusOpen     Status = "open"
	StatusResolved Status = "resolved"
	StatusIgnored  Status = "ignored"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	return s == StatusOpen || s == StatusResolved || s == StatusIgnored
}

// NotifyRule says when a recorded error is worth a message.
type NotifyRule string

const (
	// NotifyDefault takes the rule of the kind (see DefaultRule).
	NotifyDefault NotifyRule = ""
	// NotifyNever records only.
	NotifyNever NotifyRule = "never"
	// NotifyNew messages when the fingerprint was never seen (or was resolved and came back).
	NotifyNew NotifyRule = "new"
	// NotifyNewOrSpike messages for a new fingerprint and for a burst of a known one.
	NotifyNewOrSpike NotifyRule = "new_or_spike"
	// NotifySpike messages only for a burst.
	NotifySpike NotifyRule = "spike"
	// NotifyAlways messages every time; the per-fingerprint rate limit still folds a storm into one message.
	NotifyAlways NotifyRule = "always"
)

// DefaultRule is the notification trigger of a kind, from the owner's table.
func DefaultRule(k Kind) NotifyRule {
	switch k {
	case KindHTTP5xx:
		return NotifyNewOrSpike
	case KindPanic, KindJob, KindQueue, KindLabAgentOffline, KindLabDeploy, KindLabCertExpiry:
		return NotifyAlways
	case KindHTTP403, KindMail, KindLabComponent:
		return NotifyNew
	case KindHTTP429:
		return NotifySpike
	}
	return NotifyNever
}

// Event is one thing that went wrong, as the capture points describe it. Message and Stack are scrubbed when the
// journal records the event; capture points do not have to.
type Event struct {
	Kind Kind
	// Source is the route template, the job kind or the agent name. Never a raw path.
	Source  string
	Message string
	Stack   string

	Method     string
	Route      string
	HTTPStatus int
	RequestID  string
	UserID     *uuid.UUID
	Role       string
	// Permission is the permission that refused a 403; Limiter names the limiter of a 429.
	Permission string
	Limiter    string
	// Details are small extra facts (attempt, queue, agent). Values are scrubbed.
	Details map[string]string

	// Notify overrides the rule of the kind for this event.
	Notify NotifyRule
	At     time.Time
}

// Group is every occurrence of one fingerprint.
type Group struct {
	ID          uuid.UUID
	Fingerprint string
	Kind        Kind
	Source      string
	Title       string
	Status      Status
	Occurrences int64
	FirstSeenAt time.Time
	LastSeenAt  time.Time
	ResolvedAt  *time.Time
	// LastNotifiedAt and SuppressedSince drive the per-fingerprint rate limit: occurrences since the last message
	// go into the next one as a count.
	LastNotifiedAt  *time.Time
	SuppressedSince int64
}

// Sample is one recorded occurrence of a group.
type Sample struct {
	ID         uuid.UUID
	GroupID    uuid.UUID
	OccurredAt time.Time
	Message    string
	Stack      string
	Method     string
	Route      string
	HTTPStatus *int
	RequestID  string
	UserID     *uuid.UUID
	Role       string
	Permission string
	Limiter    string
	Details    map[string]string
}

// Settings are the notification settings: who gets the messages.
type Settings struct {
	// Emails are the addresses of the notification list; with EmailToSuperAdmins every super admin gets it too.
	Emails             []string
	EmailToSuperAdmins bool
	TelegramChats      []TelegramChat
	UpdatedAt          time.Time
}

// TelegramChat is one chat id (a person or a group) of the bot.
type TelegramChat struct {
	ChatID string
	Label  string
	// Failing is set when the bot got 403 (blocked, removed): the id stays visible instead of being dropped.
	Failing      bool
	FailingSince *time.Time
	LastError    string
	CreatedAt    time.Time
}

// NotFoundDay is the 404 counter of one route template on one day. An empty Route is the counter of every
// unmatched path together: paths are never stored.
type NotFoundDay struct {
	Day   time.Time
	Route string
	Hits  int64
}
