package jobsModel

import "github.com/gofrs/uuid"

// TestDeployRemovalCheckArgs is one follow-up check of a test lab that is being removed: it drops the lab's row as
// soon as the agent reports the lab (or, with the author's last lab, the group) gone, otherwise it queues the next
// check a few seconds later, up to a bound. DeployID and Attempt make a job unique (River counts a running job too, so the running
// check can still queue the next attempt, but the same attempt is never queued twice); Attempt counts the checks made so far.
type TestDeployRemovalCheckArgs struct {
	DeployID uuid.UUID `json:"deploy_id" river:"unique"`
	OwnerID  uuid.UUID `json:"owner_id"`
	Attempt  int       `json:"attempt" river:"unique"`
}

func (TestDeployRemovalCheckArgs) Kind() string { return "test_deploy_removal_check" }
