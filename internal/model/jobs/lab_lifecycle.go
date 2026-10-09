package jobsModel

// LabLifecycleArgs discovers durable lifecycle intent; wakes are advisory only.
type LabLifecycleArgs struct{}

func (LabLifecycleArgs) Kind() string { return "lab_lifecycle" }
