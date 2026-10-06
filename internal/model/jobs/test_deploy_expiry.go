package jobsModel

import "github.com/gofrs/uuid"

// TestDeployExpiryArgs ends one exercise test lab at the end of its lease. It is scheduled for the lease's
// expiry; the worker ends the lab only if the lease is still over by then (an extension moves the end, so
// the earlier job finds nothing to do).
type TestDeployExpiryArgs struct {
	DeployID uuid.UUID `json:"deploy_id"`
	OwnerID  uuid.UUID `json:"owner_id"`
}

func (TestDeployExpiryArgs) Kind() string { return "test_deploy_expiry" }
