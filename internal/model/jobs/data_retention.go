package jobsModel

// DataRetentionArgs is the empty payload of the retention purge pass.
type DataRetentionArgs struct{}

func (DataRetentionArgs) Kind() string { return "data_retention" }

// AccountInactivityArgs is the empty payload of the inactive-account pass
// (warning, then deletion after the grace period).
type AccountInactivityArgs struct{}

func (AccountInactivityArgs) Kind() string { return "account_inactivity" }
