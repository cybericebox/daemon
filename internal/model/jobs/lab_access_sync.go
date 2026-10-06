package jobsModel

// LabAccessSyncArgs drives durable default-deny Laboratory ACL reconciliation.
type LabAccessSyncArgs struct{}

func (LabAccessSyncArgs) Kind() string { return "lab_access_sync" }
