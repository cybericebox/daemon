package inboxModel

import (
	"strconv"

	"github.com/gofrs/uuid"
)

// The resource calendar flow: an organizer asks to change the reservation of an event and the platform admins
// decide; a readiness alarm asks the admins to look at a reservation that cannot be served as promised.
const (
	TypeResourceChangeRequested = "resources.change.requested"
	TypeResourceChangeApproved  = "resources.change.approved"
	TypeResourceChangeRejected  = "resources.change.rejected"
	TypeResourceAlarmRaised     = "resources.alarm.raised"
	// TypeLabWithdrawalsRepeated: a lab cluster withdrew a group of labs several times in a row because it got no
	// node in time; the admins look at the cluster capacity.
	TypeLabWithdrawalsRepeated = "resources.labs.withdrawn"
	// TypeResourceCPUThrottled tells the admins that a lab device is starved by its CPU limit.
	TypeResourceCPUThrottled = "resources.cpu.throttled"
)

func init() {
	categoryRules[TypeResourceChangeRequested] = map[RecipientRole]Category{RoleAdmin: CategoryRequests, "": CategoryPersonal}
	categoryRules[TypeResourceChangeApproved] = map[RecipientRole]Category{"": CategoryPersonal}
	categoryRules[TypeResourceChangeRejected] = map[RecipientRole]Category{"": CategoryPersonal}
	categoryRules[TypeResourceAlarmRaised] = map[RecipientRole]Category{RoleAdmin: CategoryRequests, "": CategoryPersonal}
	// An admin closes an alarm request by hand: it only says "seen", the alarm itself stays while its cause lasts.
	manuallyResolvable[TypeResourceAlarmRaised] = true
	categoryRules[TypeLabWithdrawalsRepeated] = map[RecipientRole]Category{RoleAdmin: CategoryRequests, "": CategoryPersonal}
	manuallyResolvable[TypeLabWithdrawalsRepeated] = true
	categoryRules[TypeResourceCPUThrottled] = map[RecipientRole]Category{RoleAdmin: CategoryRequests, "": CategoryPersonal}
	manuallyResolvable[TypeResourceCPUThrottled] = true
}

// ResourceChangeRef ties every admin's copy of one change request together.
func ResourceChangeRef(id uuid.UUID) string { return "resource_change:" + id.String() }

// ResourceAlarmRef ties every admin's copy of one readiness alarm together.
func ResourceAlarmRef(id uuid.UUID) string { return "resource_alarm:" + id.String() }

// LabWithdrawalRef ties every admin's copy of one alert about a withdrawal series together: the agent, the group and
// the series (the retry time of its third withdrawal, the same for every lab of the group).
func LabWithdrawalRef(agentID uuid.UUID, group string, series int64) string {
	return "lab_withdrawn:" + agentID.String() + ":" + group + ":" + strconv.FormatInt(series, 10)
}

// CPUThrottleRef ties every admin's copy of the throttling alert of one device in one lab run together.
func CPUThrottleRef(labRun, device string) string { return "cpu_throttle:" + labRun + "/" + device }
