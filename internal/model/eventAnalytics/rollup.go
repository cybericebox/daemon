package eventAnalyticsModel

import "time"

const (
	// BucketSize is the grain of the activity series.
	BucketSize = 5 * time.Minute
	// FinalizeGrace is how long after the finish the rollups keep being
	// rebuilt on every pass (late submissions received before the finish,
	// the last telemetry). After it the event is finalized; a later results
	// change (a manual decision) still triggers one rebuild.
	FinalizeGrace = 15 * time.Minute
	// ActiveWindow is how recent an action must be for a participant to
	// count as active now.
	ActiveWindow = 15 * time.Minute
)

// Final reports whether an event's rollups are final at now: the event has
// an effective finish and the grace period after it has passed.
func Final(finish *time.Time, now time.Time) bool {
	return finish != nil && !now.Before(finish.Add(FinalizeGrace))
}

// FreezeAt is when the results freeze starts: minutes before the finish,
// when the freeze is enabled and the event has a finish.
func FreezeAt(finish *time.Time, enabled bool, minutes int32) *time.Time {
	if !enabled || finish == nil || minutes <= 0 {
		return nil
	}
	at := finish.Add(-time.Duration(minutes) * time.Minute)
	return &at
}
