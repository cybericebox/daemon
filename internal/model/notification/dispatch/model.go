package dispatchModel

// DispatchStatus is the lifecycle status of a notification dispatch row.
type DispatchStatus string

const (
	DispatchStatusPending DispatchStatus = "pending"
	DispatchStatusStarted DispatchStatus = "started"
	DispatchStatusDone    DispatchStatus = "done"
)

// TargetStatus is the per-channel delivery status of a dispatch target row.
type TargetStatus string

const (
	TargetStatusDone  TargetStatus = "done"
	TargetStatusError TargetStatus = "error"
	// TargetStatusDeferred: the send limit of the SMTP transport was reached;
	// the job runs again later and the row ends as done or error.
	TargetStatusDeferred TargetStatus = "deferred"
)
