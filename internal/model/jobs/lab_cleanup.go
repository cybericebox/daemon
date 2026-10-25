package jobsModel

import "github.com/riverqueue/river"

// LabCleanupArgs drives teardown of event laboratories after withdrawal.
type LabCleanupArgs struct{}

func (LabCleanupArgs) Kind() string { return "lab_cleanup" }

// InsertOpts dedupes wakes: one pass tears down every withdrawn laboratory.
func (LabCleanupArgs) InsertOpts() river.InsertOpts { return PassInsertOpts() }
