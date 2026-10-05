package jobsModel

// LabGroupSweepArgs drives the periodic deletion of orphaned stand and test lab groups.
type LabGroupSweepArgs struct{}

func (LabGroupSweepArgs) Kind() string { return "lab_group_sweep" }
