package eventModel

import "time"

// JoinPolicy determines when registration is allowed. Registration itself
// stays in EventConfig and determines whether the join is open or moderated.
type JoinPolicy int16

const (
	JoinPolicyLockedAtStart JoinPolicy = iota
	JoinPolicyRolling
)

func (v JoinPolicy) valid() bool { return v == JoinPolicyLockedAtStart || v == JoinPolicyRolling }

// LifecycleStatus is always derived; it is never persisted as mutable state.
type LifecycleStatus int16

const (
	LifecycleNotPublished LifecycleStatus = iota
	LifecyclePublished
	LifecycleStarted
	LifecycleFinished
	LifecycleWithdrawn
)

// Lifecycle is the timing and behavior part of an event. A scheduled end is
// represented by the concrete finish/withdraw pair. Its absence means the
// event runs until a manager sets ManualFinishAt.
type Lifecycle struct {
	Configured bool // false until a manager explicitly schedules publication
	JoinPolicy JoinPolicy

	PublishAt  time.Time
	StartAt    time.Time
	FinishAt   *time.Time
	WithdrawAt *time.Time

	ManualFinishAt *time.Time
}

func NewLifecycle(joinPolicy JoinPolicy, publishAt, startAt time.Time,
	finishAt, withdrawAt, manualFinishAt *time.Time,
) (Lifecycle, error) {
	lifecycle := Lifecycle{
		Configured:     true,
		JoinPolicy:     joinPolicy,
		PublishAt:      publishAt,
		StartAt:        startAt,
		FinishAt:       cloneTime(finishAt),
		WithdrawAt:     cloneTime(withdrawAt),
		ManualFinishAt: cloneTime(manualFinishAt),
	}
	if !lifecycle.valid() {
		return Lifecycle{}, ErrEventLifecycleInvalid.Err()
	}
	return lifecycle, nil
}

func (l Lifecycle) valid() bool {
	if !l.JoinPolicy.valid() || l.PublishAt.IsZero() || l.StartAt.IsZero() || l.StartAt.Before(l.PublishAt) {
		return false
	}

	if l.FinishAt == nil && l.WithdrawAt == nil {
		return l.FinishAt == nil && l.WithdrawAt == nil && l.validManualFinish(nil)
	}
	if l.FinishAt == nil || l.WithdrawAt == nil ||
		!l.FinishAt.After(l.StartAt) || !l.WithdrawAt.After(*l.FinishAt) {
		return false
	}
	return l.validManualFinish(l.WithdrawAt)
}

func (l Lifecycle) validManualFinish(withdrawAt *time.Time) bool {
	if l.ManualFinishAt == nil {
		return true
	}
	if l.ManualFinishAt.Before(l.StartAt) {
		return false
	}
	return withdrawAt == nil || l.ManualFinishAt.Before(*withdrawAt)
}

// Status determines the public lifecycle state at a moment in time. A
// scheduled withdrawal dominates every earlier state; a manual finish takes
// effect immediately but does not erase the scheduled withdrawal.
func (l Lifecycle) Status(now time.Time) LifecycleStatus {
	if !l.Configured {
		return LifecycleNotPublished
	}
	if l.WithdrawAt != nil && !now.Before(*l.WithdrawAt) {
		return LifecycleWithdrawn
	}
	if now.Before(l.PublishAt) {
		return LifecycleNotPublished
	}
	if now.Before(l.StartAt) {
		return LifecyclePublished
	}
	if (l.ManualFinishAt != nil && !now.Before(*l.ManualFinishAt)) ||
		(l.FinishAt != nil && !now.Before(*l.FinishAt)) {
		return LifecycleFinished
	}
	return LifecycleStarted
}

// EffectiveFinishAt is the actual point at which participant runtime ends.
// A permanent event has no scheduled finish until a manager finishes it, and
// a manual finish can end a scheduled event earlier than its configured end.
func (l Lifecycle) EffectiveFinishAt() *time.Time {
	if !l.Configured {
		return nil
	}
	if l.FinishAt == nil {
		return cloneTime(l.ManualFinishAt)
	}
	if l.ManualFinishAt == nil || !l.ManualFinishAt.Before(*l.FinishAt) {
		return cloneTime(l.FinishAt)
	}
	return cloneTime(l.ManualFinishAt)
}

// HasStarted reports whether the event start has been reached. It stays true
// after finish and withdrawal; an unscheduled event has not started.
func (l Lifecycle) HasStarted(now time.Time) bool {
	return l.Configured && !now.Before(l.StartAt)
}

// RuntimeOpen permits participant activity that changes competition state:
// lab deployment and answer submission. Publication makes an event visible;
// only start makes its runtime available.
func (l Lifecycle) RuntimeOpen(now time.Time) bool {
	return l.Status(now) == LifecycleStarted
}

// JoinClosesAt is the single moment the join period ends: the start when
// joining locks at the start, otherwise the effective finish (or the scheduled
// withdrawal of an event that never finishes on its own). Registration and the
// team roster both follow it. nil means it never closes on its own.
func (l Lifecycle) JoinClosesAt() *time.Time {
	if !l.Configured {
		return nil
	}
	if l.JoinPolicy == JoinPolicyLockedAtStart {
		return cloneTime(&l.StartAt)
	}
	if finish := l.EffectiveFinishAt(); finish != nil {
		return finish
	}
	return cloneTime(l.WithdrawAt)
}

// JoinPeriodOpen reports whether the join period has not closed yet. It is the
// one shared rule behind RegistrationOpen and RosterOpen; publication is the
// only thing registration adds on top.
func (l Lifecycle) JoinPeriodOpen(now time.Time) bool {
	if !l.Configured {
		return false
	}
	if closes := l.JoinClosesAt(); closes != nil && !now.Before(*closes) {
		return false
	}
	status := l.Status(now)
	return status != LifecycleFinished && status != LifecycleWithdrawn
}

// RegistrationOpen derives the registration window independently of the
// Registration mode (closed/open/approval) configured on EventConfig: the join
// period, once the event is published.
func (l Lifecycle) RegistrationOpen(now time.Time) bool {
	return l.JoinPeriodOpen(now) && l.Status(now) != LifecycleNotPublished
}

// RosterOpen determines whether participants may change teams. The roster
// follows the join period exactly (see JoinClosesAt): a rolling event lets
// teams change while it runs and freezes once it finishes or is withdrawn, an
// event that locks joining at the start freezes its roster at the start. After
// a freeze only moderators may change it. Unlike RegistrationOpen, this does
// not require publication: a manager may assemble approved rosters before
// publishing.
func (l Lifecycle) RosterOpen(now time.Time) bool {
	return l.JoinPeriodOpen(now)
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// FormsTeamsAtStart says that every team forms by itself now: the event does not
// accept late joiners, so the start closes the rosters. With late join a team
// forms only when its captain confirms it.
func (l Lifecycle) FormsTeamsAtStart(now time.Time) bool {
	return l.JoinPolicy == JoinPolicyLockedAtStart && !l.StartAt.IsZero() && !l.StartAt.After(now)
}
