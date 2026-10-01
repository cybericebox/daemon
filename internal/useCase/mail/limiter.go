package mailUseCase

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/uuid"

	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

const (
	// maxRateWait is the longest a worker waits for its turn. A longer queue
	// (a big broadcast behind a slow provider limit) defers the message
	// instead of holding the worker past the job timeout.
	maxRateWait = 20 * time.Second
	// quotaRetryAfter is how long a message waits when the daily quota is used.
	quotaRetryAfter = 10 * time.Minute
	// quotaRecheck is how often the journal count is re-read; sends of this
	// process are added in between.
	quotaRecheck = 30 * time.Second
	quotaWindow  = 24 * time.Hour
)

// sendLimiter enforces the send limits of SMTP transports inside one process,
// keyed by transport identity: a rate limit (messages are spaced by the
// interval; concurrent workers queue behind each other) and a rolling daily
// quota counted from the delivery journal. With several daemon replicas the
// rate limit is per replica, and the quota is shared through the journal.
type sendLimiter struct {
	mu    sync.Mutex
	next  map[string]time.Time // key -> when the next message may go out
	quota map[string]quotaCount
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
}

type quotaCount struct {
	count int64
	at    time.Time
}

func newSendLimiter(now func() time.Time) *sendLimiter {
	return &sendLimiter{
		next: map[string]time.Time{}, quota: map[string]quotaCount{},
		now: now, sleep: sleepContext,
	}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// wait blocks until a message may be sent through key at the given spacing.
// It reserves its slot under the lock, so concurrent callers get distinct
// slots and the sustained rate never exceeds 1/interval. A slot further away
// than maxRateWait is not taken: the message is deferred.
func (l *sendLimiter) wait(ctx context.Context, key string, interval time.Duration) error {
	if interval <= 0 {
		return nil
	}
	l.mu.Lock()
	now := l.now()
	slot := l.next[key]
	if slot.Before(now) {
		slot = now
	}
	delay := slot.Sub(now)
	if delay > maxRateWait {
		l.mu.Unlock()
		return &dispatchModel.DeferredError{Message: dispatchModel.DeferredRateMessage, RetryAfter: delay}
	}
	l.next[key] = slot.Add(interval)
	l.mu.Unlock()
	return l.sleep(ctx, delay)
}

// used is the number of messages sent through key in the last 24 hours:
// the journal count, refreshed by load at most every quotaRecheck.
func (l *sendLimiter) used(key string, load func() (int64, error)) (int64, error) {
	l.mu.Lock()
	now := l.now()
	c, ok := l.quota[key]
	l.mu.Unlock()
	if ok && now.Sub(c.at) < quotaRecheck {
		return c.count, nil
	}
	n, err := load()
	if err != nil {
		return 0, err
	}
	l.mu.Lock()
	l.quota[key] = quotaCount{count: n, at: now}
	l.mu.Unlock()
	return n, nil
}

// sent counts one delivered message against the cached quota count.
func (l *sendLimiter) sent(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if c, ok := l.quota[key]; ok {
		c.count++
		l.quota[key] = c
	}
}

// key is the identity of a transport for limiting: the platform database
// config, the env transport, or one Event's own SMTP.
func limitKey(source string, eventID *uuid.UUID) string {
	if source == dispatchModel.TransportEvent && eventID != nil {
		return source + ":" + eventID.String()
	}
	return source
}

// throttle applies the transport's limits before a message is sent.
func (u *MailUseCase) throttle(ctx context.Context, t transport) error {
	if t.source == "" || t.limits.Unlimited() {
		return nil
	}
	key := t.limitKey()
	if q := t.limits.DailyQuota; q > 0 {
		used, err := u.limiter.used(key, func() (int64, error) {
			return u.dispatches.CountEmailDelivered(ctx, t.source, t.eventID, u.now().Add(-quotaWindow))
		})
		if err != nil {
			return err
		}
		if used >= int64(q) {
			return &dispatchModel.DeferredError{Message: dispatchModel.DeferredQuotaMessage, RetryAfter: quotaRetryAfter}
		}
	}
	return u.limiter.wait(ctx, key, t.limits.Interval())
}

// envLimits are the provider limits of the SMTP_* transport.
func (u *MailUseCase) envLimits() mailModel.Limits {
	return mailModel.Limits{PerSecond: u.env.MaxPerSecond, DailyQuota: u.env.DailyQuota}
}
