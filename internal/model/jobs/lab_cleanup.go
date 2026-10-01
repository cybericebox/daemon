package jobsModel

// LabCleanupArgs drives teardown of event laboratories after withdrawal.
type LabCleanupArgs struct{}

func (LabCleanupArgs) Kind() string { return "lab_cleanup" }
