package mailModel

import (
	"math"
	"time"
)

// Upper bounds of the send limits: far above any real provider quota, they
// only keep typos out of the database (MAIL_MAX_PER_SECOND_LIMIT and
// MAIL_DAILY_QUOTA_LIMIT, set once at start).
var (
	MaxPerSecondLimit float64 = 10000
	DailyQuotaLimit           = 1_000_000_000
)

// Limits are the send limits of one SMTP transport; zero = no limit.
type Limits struct {
	// PerSecond is the sustained message rate (may be fractional).
	PerSecond float64
	// DailyQuota is the number of messages allowed in a rolling 24 hours.
	DailyQuota int
}

// Unlimited reports that neither limit applies.
func (l Limits) Unlimited() bool { return l.PerSecond <= 0 && l.DailyQuota <= 0 }

// Interval is the minimum gap between two messages; 0 = no rate limit.
func (l Limits) Interval() time.Duration {
	if l.PerSecond <= 0 {
		return 0
	}
	return time.Duration(float64(time.Second) / l.PerSecond)
}

// LimitSource says where an effective limit comes from.
type LimitSource string

const (
	LimitSaved LimitSource = "saved"
	LimitEnv   LimitSource = "env"
	LimitNone  LimitSource = "none"
)

// LimitSources tells the origin of each limit of a transport.
type LimitSources struct {
	PerSecond  LimitSource
	DailyQuota LimitSource
}

// ResolveLimits picks each limit as saved, else env, else none.
func ResolveLimits(saved Limits, env Limits) (Limits, LimitSources) {
	out, src := Limits{}, LimitSources{PerSecond: LimitNone, DailyQuota: LimitNone}
	switch {
	case saved.PerSecond > 0:
		out.PerSecond, src.PerSecond = saved.PerSecond, LimitSaved
	case env.PerSecond > 0:
		out.PerSecond, src.PerSecond = env.PerSecond, LimitEnv
	}
	switch {
	case saved.DailyQuota > 0:
		out.DailyQuota, src.DailyQuota = saved.DailyQuota, LimitSaved
	case env.DailyQuota > 0:
		out.DailyQuota, src.DailyQuota = env.DailyQuota, LimitEnv
	}
	return out, src
}

// StoredLimits are the limits saved on cfg (zero where not set).
func (c SMTPConfig) StoredLimits() Limits {
	var l Limits
	if c.MaxPerSecond != nil {
		l.PerSecond = *c.MaxPerSecond
	}
	if c.DailyQuota != nil {
		l.DailyQuota = *c.DailyQuota
	}
	return l
}

// NormalizeLimits validates edited limits: nil or 0 clears a limit, anything
// else must be positive (the per-second value may be fractional).
func NormalizeLimits(perSecond *float64, daily *int) (*float64, *int, error) {
	if perSecond != nil {
		v := *perSecond
		switch {
		case math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > MaxPerSecondLimit:
			return nil, nil, ErrSMTPSettingsInvalid.WithMessage("Messages per second must be greater than 0").Err()
		case v == 0:
			perSecond = nil
		}
	}
	if daily != nil {
		v := *daily
		switch {
		case v < 0 || v > DailyQuotaLimit:
			return nil, nil, ErrSMTPSettingsInvalid.WithMessage("Daily limit must be greater than 0").Err()
		case v == 0:
			daily = nil
		}
	}
	return perSecond, daily, nil
}

// ConfigureLimitCaps sets the upper bounds of the send limits once at start.
func ConfigureLimitCaps(perSecond float64, daily int) {
	MaxPerSecondLimit, DailyQuotaLimit = perSecond, daily
}
