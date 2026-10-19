package labMonitoring

import (
	"encoding/json"
	"time"
)

// Launch is the launch-pacing and image state of one lab group (or of a whole stand) read
// from the current monitoring payload: how many of its labs wait in the operator's scheduler
// queue, where the best placed one stands, how many wait for a cluster node and whether an
// image is pulled by tag.
type Launch struct {
	QueuedLabs int
	// Position and Length belong to the queued lab with the smallest position; Reason is why it waits.
	Position, Length int32
	Reason           string
	// Attempt and RetryAt are set when that lab was withdrawn (Reason Withdrawn): how often in a row its group got no node
	// in time, and when the cluster sends it again.
	Attempt int32
	RetryAt time.Time
	// NodeStartingLabs are the labs whose pods are all sent while some wait for a node (Reason NodeStarting); WaitingSince is
	// the earliest dispatch of such a pod.
	NodeStartingLabs int
	WaitingSince     time.Time
	// ImageWarning is true when a lab or the group has an image that could not be pinned to a digest.
	ImageWarning bool
}

// Add folds another group's state into l.
func (l *Launch) Add(o Launch) {
	if o.QueuedLabs > 0 && (l.QueuedLabs == 0 || o.Position < l.Position) {
		l.Position, l.Length, l.Reason, l.Attempt, l.RetryAt = o.Position, o.Length, o.Reason, o.Attempt, o.RetryAt
	}
	l.QueuedLabs += o.QueuedLabs
	if o.NodeStartingLabs > 0 && (l.WaitingSince.IsZero() || o.WaitingSince.Before(l.WaitingSince)) {
		l.WaitingSince = o.WaitingSince
	}
	l.NodeStartingLabs += o.NodeStartingLabs
	l.ImageWarning = l.ImageWarning || o.ImageWarning
}

// podScheduling is the scheduler state of one device pod in a monitoring payload (protojson names).
type podScheduling struct {
	State        string  `json:"state"`
	DispatchedMs flexInt `json:"dispatchedUnixMs"`
	Withdrawals  int32   `json:"withdrawals"`
	RetryAfterMs flexInt `json:"retryAfterUnixMs"`
}

// PayloadLaunch reads the launch queue and image warnings of one lab group's current
// monitoring state. A payload that cannot be read counts as nothing observed.
func PayloadLaunch(raw json.RawMessage) Launch {
	var payload struct {
		Groups []struct {
			Status struct {
				ImageWarning string `json:"imageWarning"`
			} `json:"status"`
		} `json:"groups"`
		Labs []struct {
			Status struct {
				Phase        string `json:"phase"`
				ImageWarning string `json:"imageWarning"`
				Scheduling   *struct {
					Position flexInt `json:"position"`
					Length   flexInt `json:"length"`
					Reason   string  `json:"reason"`
				} `json:"scheduling"`
				Devices []struct {
					Scheduling *podScheduling `json:"scheduling"`
				} `json:"devices"`
			} `json:"status"`
		} `json:"labs"`
	}
	var out Launch
	if json.Unmarshal(raw, &payload) != nil {
		return out
	}
	for _, g := range payload.Groups {
		out.ImageWarning = out.ImageWarning || g.Status.ImageWarning != ""
	}
	for _, lab := range payload.Labs {
		out.ImageWarning = out.ImageWarning || lab.Status.ImageWarning != ""
		q := lab.Status.Scheduling
		if q == nil {
			continue
		}
		var pods []*podScheduling
		for _, d := range lab.Status.Devices {
			if d.Scheduling != nil {
				pods = append(pods, d.Scheduling)
			}
		}
		if lab.Status.Phase != "Queued" {
			if q.Reason == "NodeStarting" {
				out.Add(Launch{NodeStartingLabs: 1, WaitingSince: nodeWaitSince(pods)})
			}
			continue
		}
		one := Launch{QueuedLabs: 1, Position: int32(q.Position), Length: int32(q.Length), Reason: q.Reason}
		for _, p := range pods {
			one.Attempt = max(one.Attempt, p.Withdrawals)
			if at := msTime(p.RetryAfterMs); at.After(one.RetryAt) {
				one.RetryAt = at
			}
		}
		out.Add(one)
	}
	return out
}

// nodeWaitSince is the earliest dispatch of a pod still starting; zero when none is known.
func nodeWaitSince(pods []*podScheduling) time.Time {
	var since time.Time
	for _, p := range pods {
		at := msTime(p.DispatchedMs)
		if p.State != "POD_STATE_STARTING" || at.IsZero() {
			continue
		}
		if since.IsZero() || at.Before(since) {
			since = at
		}
	}
	return since
}

func msTime(ms flexInt) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(int64(ms))
}
