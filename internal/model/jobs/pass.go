package jobsModel

import (
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
)

// PassInsertOpts keeps at most one copy of an argument-less pass job queued or
// running: a pass covers all pending work, so a wake or a periodic tick while one
// waits adds nothing, and copies piling up behind slow passes starve the queue.
func PassInsertOpts() river.InsertOpts {
	return river.InsertOpts{
		MaxAttempts: 1,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable, rivertype.JobStatePending, rivertype.JobStateRunning,
				rivertype.JobStateRetryable, rivertype.JobStateScheduled,
			},
		},
	}
}
