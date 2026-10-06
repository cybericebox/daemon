// Package labview holds the response shapes of a lab's live state that several handlers share:
// the launch queue, the devices with their snapshot state and the image warnings. All JSON is
// PascalCase like the rest of the API.
package labview

import (
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labMonitoring "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

type (
	// QueueResponse is the place of a lab in the scheduler queue; the phase is Queued while none of its
	// pods has been dispatched. Position is 1-based among the labs that still have pods to dispatch and
	// 0 when all are dispatched (Length is their number). Reason is why the next pod waits: InFlightLimit,
	// WaitingForGroup, WaitingForTurn, PreparingImages, InsufficientResources, NoSchedulableNodes or TenantQuota; empty
	// when nothing waits. Message is its detail. Pods are the pods of the lab, Pending those not dispatched.
	QueueResponse struct {
		Position int32  `json:"Position"`
		Length   int32  `json:"Length"`
		Reason   string `json:"Reason"`
		Message  string `json:"Message"`
		Pods     int32  `json:"Pods"`
		Pending  int32  `json:"Pending"`
	}

	// SchedulingResponse is one device pod on its way through the scheduler. State is Queued, Starting,
	// Started or Failed; the times are absent until they happen. Failure is set for a pod that did not
	// start in time (it may still become Ready) and clears when it does.
	SchedulingResponse struct {
		State        string           `json:"State"`
		QueuedAt     *time.Time       `json:"QueuedAt"`
		DispatchedAt *time.Time       `json:"DispatchedAt"`
		StartedAt    *time.Time       `json:"StartedAt"`
		Failure      *FailureResponse `json:"Failure"`
	}

	// FailureResponse explains a pod that did not start. Reason is ImagePull, CrashLoop, Unschedulable,
	// StartupTimeout or DoesNotFit; Message is the last error the node reported.
	FailureResponse struct {
		Reason       string     `json:"Reason"`
		Message      string     `json:"Message"`
		RestartCount int32      `json:"RestartCount"`
		At           *time.Time `json:"At"`
	}

	// StandQueueResponse summarizes the labs of one stand that wait in the launch queue: how many, and
	// the place and reason of the best placed one. Reason is as in QueueResponse.
	StandQueueResponse struct {
		QueuedLabs int    `json:"QueuedLabs"`
		Position   int32  `json:"Position"`
		Length     int32  `json:"Length"`
		Reason     string `json:"Reason"`
	}

	// SnapshotResponse is the writable-layer snapshot state of a device with state persistence.
	// The times are absent when the snapshot or the restore never happened.
	SnapshotResponse struct {
		LastSnapshotAt *time.Time `json:"LastSnapshotAt"`
		RestoredAt     *time.Time `json:"RestoredAt"`
		SizeBytes      int64      `json:"SizeBytes"`
		Warning        string     `json:"Warning"`
		Rescue         bool       `json:"Rescue"`
	}

	// DeviceResponse is one device of a lab. Snapshot is null for a device without state persistence.
	DeviceResponse struct {
		Name   string `json:"Name"`
		Ready  bool   `json:"Ready"`
		Reason string `json:"Reason"`
		// Type is the device type: container, unmanaged-switch, hub, vpn or internet; empty when unknown.
		Type string `json:"Type,omitempty"`
		// LogicalName is the device name in the exercise topology; empty when unknown.
		LogicalName string `json:"LogicalName,omitempty"`
		// Scheduling is null when the scheduler does not track the device.
		Scheduling *SchedulingResponse `json:"Scheduling"`
		Snapshot   *SnapshotResponse   `json:"Snapshot"`
	}

	// StatusResponse is the live state of one lab for organizers and administrators.
	StatusResponse struct {
		Phase string `json:"Phase"`
		Ready bool   `json:"Ready"`
		// Queue is null for a lab that was never queued.
		Queue *QueueResponse `json:"Queue"`
		// ImageWarning lists the lab images pulled by tag (not pinned to a digest); GroupImageWarning
		// is the same for the group's VPN or gateway image. Empty when fine.
		ImageWarning      string           `json:"ImageWarning"`
		GroupImageWarning string           `json:"GroupImageWarning"`
		Devices           []DeviceResponse `json:"Devices"`
	}
)

// Queue maps the launch queue state; nil stays nil.
func Queue(q *exerciseModel.LabQueue) *QueueResponse {
	if q == nil {
		return nil
	}
	return &QueueResponse{Position: q.Position, Length: q.Length, Reason: q.Reason, Message: q.Message, Pods: q.Pods, Pending: q.Pending}
}

// StandQueue summarizes the queue of a stand from the monitoring state; nil when no lab waits.
func StandQueue(l labMonitoring.Launch) *StandQueueResponse {
	if l.QueuedLabs == 0 {
		return nil
	}
	return &StandQueueResponse{QueuedLabs: l.QueuedLabs, Position: l.Position, Length: l.Length, Reason: l.Reason}
}

// Snapshot maps a device snapshot; nil stays nil.
func Snapshot(s *exerciseModel.DeviceSnapshot) *SnapshotResponse {
	if s == nil {
		return nil
	}
	return &SnapshotResponse{LastSnapshotAt: optionalTime(s.LastSnapshotAt), RestoredAt: optionalTime(s.RestoredAt), SizeBytes: s.SizeBytes, Warning: s.Warning, Rescue: s.Rescue}
}

// Scheduling maps the scheduler state of a device pod; nil stays nil.
func Scheduling(p *exerciseModel.PodScheduling) *SchedulingResponse {
	if p == nil {
		return nil
	}
	out := &SchedulingResponse{State: p.State, QueuedAt: optionalTime(p.QueuedAt), DispatchedAt: optionalTime(p.DispatchedAt), StartedAt: optionalTime(p.StartedAt)}
	if f := p.Failure; f != nil {
		out.Failure = &FailureResponse{Reason: f.Reason, Message: f.Message, RestartCount: f.RestartCount, At: optionalTime(f.At)}
	}
	return out
}

// Device maps one device.
func Device(d exerciseModel.LabDeployedDevice) DeviceResponse {
	return DeviceResponse{Name: d.Name, Ready: d.Ready, Reason: d.Reason, Scheduling: Scheduling(d.Scheduling), Snapshot: Snapshot(d.Snapshot)}
}

// Status maps the live state of a lab.
func Status(s exerciseModel.LabDeployStatus) StatusResponse {
	out := StatusResponse{
		Phase: s.Phase, Ready: s.Ready, Queue: Queue(s.Queue), ImageWarning: s.ImageWarning,
		GroupImageWarning: s.GroupImageWarning, Devices: make([]DeviceResponse, 0, len(s.Devices)),
	}
	for _, d := range s.Devices {
		out.Devices = append(out.Devices, Device(d))
	}
	return out
}

func optionalTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// DeviceInfo is what the exercise topology says about one lab device.
type DeviceInfo struct {
	Name string
	Type string
}

// Gateway device names the lab operator reserves for the lab's VPN and internet gateways.
const (
	gatewayVPN      = "vpn"
	gatewayInternet = "internet"
)

// AnnotateDevices fills the type and the logical name of every device from the topology, keyed by
// the device name in the lab. A device the topology does not know keeps them empty, except the
// reserved VPN and internet gateways, which have no topology device and get their own type.
func AnnotateDevices(s *StatusResponse, topology map[string]DeviceInfo) {
	for i := range s.Devices {
		d := &s.Devices[i]
		if info, ok := topology[d.Name]; ok {
			d.Type, d.LogicalName = info.Type, info.Name
			continue
		}
		if d.Name == gatewayVPN || d.Name == gatewayInternet {
			d.Type = d.Name
		}
	}
}
