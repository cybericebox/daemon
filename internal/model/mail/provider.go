package mailModel

import (
	"errors"
	"net"
	"net/textproto"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid"
)

// MaxProviderNameLength bounds the provider display name.
const MaxProviderNameLength = 64

// ErrorCooldown is how long a provider that just failed is tried after the
// healthy ones: a broken top provider does not cost every message a connection.
const ErrorCooldown = time.Minute

// Usage is the daily counter of one provider. Day is the UTC date the count
// belongs to; a count of an earlier day is zero today (the counter restarts
// lazily on the first send of a new day).
type Usage struct {
	Day         time.Time
	SentToday   int
	LastUsedAt  *time.Time
	LastError   string
	LastErrorAt *time.Time
}

// Today is the UTC date of now, the counter day.
func Today(now time.Time) time.Time {
	y, m, d := now.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// SentOn is the number of messages sent through the provider on the UTC day of now.
func (u Usage) SentOn(now time.Time) int {
	if u.Day.IsZero() || !Today(u.Day).Equal(Today(now)) {
		return 0
	}
	return u.SentToday
}

// HasCapacity reports whether the provider may send one more message today.
func (c SMTPConfig) HasCapacity(now time.Time) bool {
	return c.DailyQuota == nil || c.Usage.SentOn(now) < *c.DailyQuota
}

// cooling reports a provider whose last attempt failed less than ErrorCooldown ago.
func (c SMTPConfig) cooling(now time.Time) bool {
	u := c.Usage
	if u.LastErrorAt == nil || now.Sub(*u.LastErrorAt) >= ErrorCooldown {
		return false
	}
	return u.LastUsedAt == nil || u.LastUsedAt.Before(*u.LastErrorAt)
}

// SelectProviders is the order a message tries the platform providers: enabled
// ones with daily capacity left, by priority (then creation), providers in
// error cooldown last. Providers over their daily limit are left out.
func SelectProviders(all []SMTPConfig, now time.Time) []SMTPConfig {
	var healthy, cooling []SMTPConfig
	for _, p := range all {
		if !p.Enabled || !p.HasCapacity(now) {
			continue
		}
		if p.cooling(now) {
			cooling = append(cooling, p)
		} else {
			healthy = append(healthy, p)
		}
	}
	byPriority := func(l []SMTPConfig) {
		sort.SliceStable(l, func(i, j int) bool {
			if l[i].Priority != l[j].Priority {
				return l[i].Priority < l[j].Priority
			}
			if !l[i].CreatedAt.Equal(l[j].CreatedAt) {
				return l[i].CreatedAt.Before(l[j].CreatedAt)
			}
			return l[i].ID.String() < l[j].ID.String()
		})
	}
	byPriority(healthy)
	byPriority(cooling)
	return append(healthy, cooling...)
}

// AllExhausted reports that providers are enabled but every one of them is over
// its daily limit (sending waits for the next UTC day).
func AllExhausted(all []SMTPConfig, now time.Time) bool {
	enabled := 0
	for _, p := range all {
		if !p.Enabled {
			continue
		}
		enabled++
		if p.HasCapacity(now) {
			return false
		}
	}
	return enabled > 0
}

// NextReset is the start of the next UTC day, when daily counters restart.
func NextReset(now time.Time) time.Time { return Today(now).Add(24 * time.Hour) }

// ProviderFailure tells whether a send error is the provider's own problem
// (authentication, quota, outage): the next provider is tried and the error is
// kept on the provider. A rejected recipient or message (550, 553, ...) fails
// the same way everywhere and is not a provider failure.
func ProviderFailure(err error) bool {
	if err == nil {
		return false
	}
	if tp, ok := errors.AsType[*textproto.Error](err); ok {
		switch tp.Code {
		case 421, 452, 530, 534, 535, 538, 552, 554:
			return true
		}
		return false
	}
	if _, ok := errors.AsType[net.Error](err); ok {
		return true
	}
	// Dial, TLS and timeouts surface as plain wrapped errors.
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{"connection", "timeout", "tls", "dial", "eof", "no such host", "refused"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// ProviderInput is an edit of one platform provider: the connection, the
// optional sender and the state.
type ProviderInput struct {
	SMTPInput
	Name string
	// Enabled nil keeps the stored state (a new provider is enabled).
	Enabled  *bool
	Identity Identity
}

// Normalize trims and validates the provider fields.
func (in ProviderInput) Normalize() (ProviderInput, error) {
	var err error
	if in.SMTPInput, err = in.SMTPInput.Normalize(); err != nil {
		return in, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > MaxProviderNameLength || strings.ContainsAny(in.Name, "\r\n") {
		return in, ErrSMTPSettingsInvalid.WithMessage("Provider name is required and must be at most 64 characters").Err()
	}
	if in.Identity, err = in.Identity.Normalize(); err != nil {
		return in, err
	}
	return in, nil
}

// ProviderOrder is the full id order for a reorder: the ids given first (those
// that exist, once each), then the other providers in their current order.
func ProviderOrder(all []SMTPConfig, ids []uuid.UUID) []uuid.UUID {
	exists := make(map[uuid.UUID]bool, len(all))
	for _, p := range all {
		exists[p.ID] = true
	}
	out := make([]uuid.UUID, 0, len(all))
	seen := make(map[uuid.UUID]bool, len(all))
	for _, id := range ids {
		if exists[id] && !seen[id] {
			out, seen[id] = append(out, id), true
		}
	}
	for _, p := range all {
		if !seen[p.ID] {
			out = append(out, p.ID)
		}
	}
	return out
}
