package exerciseModel

import (
	"github.com/gofrs/uuid"
	"time"
)

type TestDeploy struct {
	ID, VersionID, VariantID, CreatedBy uuid.UUID
	// EventID keeps participant preview authorization and resource scope across requests.
	EventID *uuid.UUID
	// GroupName is the author's lab group (shared by all their test labs); LabName is this deploy's Lab in it.
	GroupName, LabName   string
	CreatedAt, ExpiresAt time.Time
	// Flags are the test values injected into the linked devices, kept for the author.
	Flags []DeployFlag
	// Solved are the tasks the author has checked correctly in this deploy.
	Solved []uuid.UUID
	// ExerciseID is the exercise of VersionID; filled when deploys are listed, not stored.
	ExerciseID uuid.UUID
	// Expired is true when the lease is over but the lab is not removed yet (filled when deploys are listed, not stored).
	Expired bool
	// Removing is true while the lab is being removed: the lease is over and the agent still has it (filled when listed, not stored).
	Removing bool
	// EndingAt is set once the run's cluster reservation gave its ending notice: the lab is deleted then unless
	// extended (an extension clears it).
	EndingAt *time.Time
	// Length is the run length the author chose: the lease, counted only while the lab runs (zero: not kept, for a row
	// made before it was).
	Length time.Duration
}

// Kinds of a test deploy. The kind follows the row shape, never the live variant: a draft variant may gain or lose
// devices after the row is made.
const (
	DeployKindLab    = "lab"
	DeployKindStatic = "static"
)

// IsStatic is true for a test of a variant without devices: no lab, no group, only the lease and the flags.
func (d TestDeploy) IsStatic() bool {
	return d.EventID == nil && d.GroupName == "" && d.LabName == ""
}

// Kind is DeployKindStatic or DeployKindLab.
func (d TestDeploy) Kind() string {
	if d.IsStatic() {
		return DeployKindStatic
	}
	return DeployKindLab
}

// NewStaticTestDeploy makes the row of a device-less test: empty group and lab names, the lease starting at now.
func NewStaticTestDeploy(id, versionID, variantID, owner uuid.UUID, flags []DeployFlag, now time.Time, lease time.Duration) TestDeploy {
	return TestDeploy{
		ID: id, VersionID: versionID, VariantID: variantID, CreatedBy: owner,
		CreatedAt: now, ExpiresAt: now.Add(lease), Flags: flags,
	}
}

// TestLeaseEnd is when a test lab's lease ends, read from its live status at the end it has now: the lease counts only
// while the lab runs. A lab the cluster has not started yet (queued, its pods not all sent, waiting for a node, or
// withdrawn and queued again) gets a fresh lease from now; a running lab's lease counts from when its last pod started.
// limit is the hard end (zero: none). extend is false when the lease is over: the lab ran its lease, it is broken
// (a failure is not the cluster's wait), or the limit is reached.
func TestLeaseEnd(status LabDeployStatus, lease time.Duration, limit, now time.Time) (due time.Time, extend bool) {
	switch {
	case status.Ready:
		var started time.Time
		for _, d := range status.Devices {
			if d.Scheduling != nil && d.Scheduling.StartedAt.After(started) {
				started = d.Scheduling.StartedAt
			}
		}
		if started.IsZero() {
			return time.Time{}, false
		}
		due = started.Add(lease)
	case status.Phase == DeployPhaseQueued || status.Queue != nil && (status.Queue.Pending > 0 ||
		status.Queue.Reason == QueueReasonNodeStarting || status.Queue.Reason == QueueReasonWithdrawn):
		due = now.Add(lease)
	default:
		return time.Time{}, false
	}
	if !limit.IsZero() && due.After(limit) {
		due = limit
	}
	if !due.After(now) {
		return time.Time{}, false
	}
	return due, true
}
