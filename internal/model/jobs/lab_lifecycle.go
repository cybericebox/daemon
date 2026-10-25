package jobsModel

import "github.com/riverqueue/river"

// LabLifecycleArgs discovers durable lifecycle intent; wakes are advisory only.
type LabLifecycleArgs struct{}

func (LabLifecycleArgs) Kind() string { return "lab_lifecycle" }

// InsertOpts dedupes wakes: one pass discovers all persisted intent.
func (LabLifecycleArgs) InsertOpts() river.InsertOpts { return PassInsertOpts() }
