package eventAnalyticsModel

import (
	"encoding/json"
	"sort"
	"strconv"
	"time"

	"github.com/gofrs/uuid"
)

// Lab transition kinds written to the journal.
const (
	LabTransitionLabReady         = "lab_ready"
	LabTransitionLabNotReady      = "lab_not_ready"
	LabTransitionDeviceReady      = "device_ready"
	LabTransitionDeviceRestart    = "device_restart"
	LabTransitionPodScheduled     = "pod_scheduled"
	LabTransitionPodStarted       = "pod_started"
	LabTransitionVPNBootChanged   = "vpn_boot_changed"
	LabTransitionPolicyApplied    = "policy_applied"
	LabTransitionClientConnected  = "client_connected"
	LabTransitionClientDisconnect = "client_disconnected"
)

// Entity kinds of the journal state.
const (
	labEntityLab    = "lab"
	labEntityDevice = "device"
	labEntityPod    = "pod" // a pod the lab group runs itself ("vpn", "gateway")
	labEntityGroup  = "group"
	labEntityPolicy = "policy"
	labEntityClient = "client"
)

// LabTransition is one change seen in the telemetry. At is the agent's time,
// ReceivedAt the platform's time of the observation that showed it.
type LabTransition struct {
	ID           uuid.UUID `json:"id"`
	TeamID       uuid.UUID `json:"team_id"`
	LabGroupName string    `json:"lab_group_name"`
	LabName      string    `json:"lab_name"`
	DeviceName   *string   `json:"device_name"`
	ClientName   *string   `json:"client_name"`
	Kind         string    `json:"kind"`
	At           time.Time `json:"at"`
	ReceivedAt   time.Time `json:"received_at"`
	Detail       *string   `json:"detail"`
}

// LabEntityKey names one tracked entity of a team's lab group.
type LabEntityKey struct {
	TeamID       uuid.UUID `json:"team_id"`
	LabGroupName string    `json:"lab_group_name"`
	Kind         string    `json:"kind"`
	LabName      string    `json:"lab_name"`
	Name         string    `json:"name"`
}

// LabEntityState is what the journal remembers of an entity to recognize the
// next change. Fields are used by kind: ready, phase, restarts and the
// scheduling times by labs, devices and pods; boot by the group; applied by a
// policy; connected and handshake by a client.
type LabEntityState struct {
	Ready      bool   `json:"ready,omitempty"`
	Phase      string `json:"phase,omitempty"`
	Restarts   int64  `json:"restarts,omitempty"`
	Dispatched int64  `json:"dispatched,omitempty"`
	Started    int64  `json:"started,omitempty"`
	BootID     string `json:"boot,omitempty"`
	Applied    int64  `json:"applied,omitempty"`
	Connected  bool   `json:"connected,omitempty"`
	Handshake  int64  `json:"handshake,omitempty"`
}

// LabStateRow is a stored entity state.
type LabStateRow struct {
	LabEntityKey

	State LabEntityState `json:"state"`
}

// LabJournal finds the transitions between consecutive observations. It is
// seeded with the stored states, applied to observations in arrival order, and
// hands back the changed states to store with the transitions.
type LabJournal struct {
	// Quiet records the states without writing transitions: the first look at an event that is over only sees its
	// last snapshots, whose "became ready" would be an invented history.
	Quiet   bool
	states  map[LabEntityKey]LabEntityState
	dirty   map[LabEntityKey]struct{}
	clients map[labGroupKey]map[LabEntityKey]struct{}
	newID   func() uuid.UUID
}

type labGroupKey struct {
	team  uuid.UUID
	group string
}

// NewLabJournal starts a journal from the stored states.
func NewLabJournal(stored []LabStateRow, newID func() uuid.UUID) *LabJournal {
	j := &LabJournal{
		states: map[LabEntityKey]LabEntityState{}, dirty: map[LabEntityKey]struct{}{},
		clients: map[labGroupKey]map[LabEntityKey]struct{}{}, newID: newID,
	}
	for _, row := range stored {
		j.states[row.LabEntityKey] = row.State
		j.trackClient(row.LabEntityKey)
	}
	return j
}

func (j *LabJournal) trackClient(k LabEntityKey) {
	if k.Kind != labEntityClient {
		return
	}
	g := labGroupKey{k.TeamID, k.LabGroupName}
	if j.clients[g] == nil {
		j.clients[g] = map[LabEntityKey]struct{}{}
	}
	j.clients[g][k] = struct{}{}
}

// Changed returns the states changed since the last call and forgets them.
func (j *LabJournal) Changed() []LabStateRow {
	out := make([]LabStateRow, 0, len(j.dirty))
	for k := range j.dirty {
		out = append(out, LabStateRow{LabEntityKey: k, State: j.states[k]})
	}
	j.dirty = map[LabEntityKey]struct{}{}
	return out
}

func (j *LabJournal) set(k LabEntityKey, s LabEntityState) {
	if old, ok := j.states[k]; ok && old == s {
		return
	}
	j.states[k] = s
	j.dirty[k] = struct{}{}
	j.trackClient(k)
}

// flexInt reads a JSON integer written as a number or, as protojson does for
// 64-bit fields, as a string.
type flexInt int64

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		var fl float64
		if ferr := json.Unmarshal([]byte(s), &fl); ferr != nil {
			return nil //nolint:nilerr // an unreadable counter is treated as absent
		}
		n = int64(fl)
	}
	*f = flexInt(n)
	return nil
}

type (
	podScheduling struct {
		DispatchedUnixMs flexInt `json:"dispatchedUnixMs"`
		StartedUnixMs    flexInt `json:"startedUnixMs"`
	}
	journalPayload struct {
		Labs []struct {
			Name   string `json:"name"`
			Status struct {
				Ready   bool `json:"ready"`
				Devices []struct {
					Name         string         `json:"name"`
					Ready        bool           `json:"ready"`
					PodPhase     string         `json:"podPhase"`
					RestartCount flexInt        `json:"restartCount"`
					Scheduling   *podScheduling `json:"scheduling"`
				} `json:"devices"`
			} `json:"status"`
		} `json:"labs"`
		Groups []struct {
			Name   string `json:"name"`
			Status struct {
				CurrentVPNBootID string `json:"currentVpnBootId"`
				Pods             []struct {
					Name       string         `json:"name"`
					Scheduling *podScheduling `json:"scheduling"`
				} `json:"pods"`
			} `json:"status"`
		} `json:"groups"`
		Clients []struct {
			Name   string `json:"name"`
			Status struct {
				Statistics *struct {
					LastHandshakeUnix flexInt `json:"lastHandshakeUnix"`
				} `json:"statistics"`
			} `json:"status"`
		} `json:"clients"`
		Policies []struct {
			PolicyUID string `json:"policyUid"`
			Status    struct {
				AppliedAtUnixMs flexInt `json:"appliedAtUnixMs"`
				AppliedRevision flexInt `json:"appliedRevision"`
			} `json:"status"`
		} `json:"policies"`
	}
)

// ClientConnected reports whether a peer with the given last handshake (unix
// seconds, 0 = never) is online at observedAt.
func ClientConnected(handshakeUnix int64, observedAt time.Time) bool {
	if handshakeUnix <= 0 || handshakeUnix > 4102444800 {
		return false
	}
	return observedAt.Sub(time.Unix(handshakeUnix, 0)) <= VPNOnlineWindow
}

// Apply folds one observation in and returns the transitions it shows. An
// observation that cannot be read changes nothing.
func (j *LabJournal) Apply(o LabObservation) []LabTransition {
	var p journalPayload
	if err := json.Unmarshal(o.Payload, &p); err != nil {
		return nil
	}
	var out []LabTransition
	emit := func(kind, lab string, device, client *string, at time.Time, detail string) {
		t := LabTransition{
			ID: j.newID(), TeamID: o.EventTeamID, LabGroupName: o.LabGroupName, LabName: lab,
			DeviceName: device, ClientName: client, Kind: kind, At: at, ReceivedAt: o.ReceivedAt,
		}
		if detail != "" {
			t.Detail = &detail
		}
		out = append(out, t)
	}
	key := func(kind, lab, name string) LabEntityKey {
		return LabEntityKey{TeamID: o.EventTeamID, LabGroupName: o.LabGroupName, Kind: kind, LabName: lab, Name: name}
	}
	scheduling := func(s LabEntityState, sch *podScheduling, lab string, device *string) LabEntityState {
		if sch == nil {
			return s
		}
		if d := int64(sch.DispatchedUnixMs); d > 0 && d != s.Dispatched {
			emit(LabTransitionPodScheduled, lab, device, nil, time.UnixMilli(d).UTC(), "")
			s.Dispatched = d
		}
		if st := int64(sch.StartedUnixMs); st > 0 && st != s.Started {
			emit(LabTransitionPodStarted, lab, device, nil, time.UnixMilli(st).UTC(), "")
			s.Started = st
		}
		return s
	}

	for _, lab := range p.Labs {
		k := key(labEntityLab, lab.Name, lab.Name)
		prev, known := j.states[k]
		s := prev
		s.Ready = lab.Status.Ready
		if s.Ready && (!known || !prev.Ready) {
			emit(LabTransitionLabReady, lab.Name, nil, nil, o.ObservedAt, "")
		} else if !s.Ready && known && prev.Ready {
			emit(LabTransitionLabNotReady, lab.Name, nil, nil, o.ObservedAt, "")
		}
		j.set(k, s)
		for _, dev := range lab.Status.Devices {
			device := dev.Name
			dk := key(labEntityDevice, lab.Name, dev.Name)
			dprev, dknown := j.states[dk]
			ds := dprev
			ds.Ready, ds.Phase = dev.Ready, dev.PodPhase
			if ds.Ready && (!dknown || !dprev.Ready) {
				emit(LabTransitionDeviceReady, lab.Name, &device, nil, o.ObservedAt, dev.PodPhase)
			}
			if restarts := int64(dev.RestartCount); restarts != dprev.Restarts {
				if dknown && restarts > dprev.Restarts {
					emit(LabTransitionDeviceRestart, lab.Name, &device, nil, o.ObservedAt, dev.PodPhase)
				}
				ds.Restarts = restarts
			}
			ds = scheduling(ds, dev.Scheduling, lab.Name, &device)
			j.set(dk, ds)
		}
	}
	for _, g := range p.Groups {
		name := g.Name
		if name == "" {
			name = o.LabGroupName
		}
		gk := key(labEntityGroup, "", name)
		gprev := j.states[gk]
		gs := gprev
		if boot := g.Status.CurrentVPNBootID; boot != "" && boot != gprev.BootID {
			emit(LabTransitionVPNBootChanged, "", nil, nil, o.ObservedAt, boot)
			gs.BootID = boot
		}
		j.set(gk, gs)
		for _, pod := range g.Status.Pods {
			device := pod.Name
			pk := key(labEntityPod, "", pod.Name)
			pprev := j.states[pk]
			j.set(pk, scheduling(pprev, pod.Scheduling, "", &device))
		}
	}
	for _, pol := range p.Policies {
		name := pol.PolicyUID
		if name == "" {
			name = "policy"
		}
		pk := key(labEntityPolicy, "", name)
		prev := j.states[pk]
		if applied := int64(pol.Status.AppliedAtUnixMs); applied > 0 && applied != prev.Applied {
			detail := "revision " + strconv.FormatInt(int64(pol.Status.AppliedRevision), 10)
			emit(LabTransitionPolicyApplied, "", nil, nil, time.UnixMilli(applied).UTC(), detail)
			prev.Applied = applied
			j.set(pk, prev)
		}
	}

	present := map[LabEntityKey]struct{}{}
	for _, c := range p.Clients {
		if c.Name == "" {
			continue
		}
		client := c.Name
		ck := key(labEntityClient, "", c.Name)
		present[ck] = struct{}{}
		prev, known := j.states[ck]
		s := prev
		if c.Status.Statistics != nil {
			s.Handshake = int64(c.Status.Statistics.LastHandshakeUnix)
		}
		s.Connected = ClientConnected(s.Handshake, o.ObservedAt)
		switch {
		case s.Connected && !prev.Connected:
			at := o.ObservedAt
			if hs := time.Unix(s.Handshake, 0).UTC(); hs.Before(at) {
				at = hs
			}
			emit(LabTransitionClientConnected, "", nil, &client, at, "")
		case !s.Connected && known && prev.Connected:
			emit(LabTransitionClientDisconnect, "", nil, &client, disconnectAt(s.Handshake, o.ObservedAt), "")
		}
		j.set(ck, s)
	}
	// A delta lists only the clients that changed: a connected client not
	// listed whose last handshake has aged out is disconnected.
	var tracked []LabEntityKey
	for ck := range j.clients[labGroupKey{o.EventTeamID, o.LabGroupName}] {
		if _, listed := present[ck]; !listed {
			tracked = append(tracked, ck)
		}
	}
	sort.Slice(tracked, func(a, b int) bool { return tracked[a].Name < tracked[b].Name })
	for _, ck := range tracked {
		s := j.states[ck]
		if !s.Connected || ClientConnected(s.Handshake, o.ObservedAt) {
			continue
		}
		client := ck.Name
		emit(LabTransitionClientDisconnect, "", nil, &client, disconnectAt(s.Handshake, o.ObservedAt), "")
		s.Connected = false
		j.set(ck, s)
	}
	if j.Quiet {
		return nil
	}
	return out
}

// disconnectAt is when a peer with the given last handshake went offline: the
// end of its online window, but not later than the observation that showed it.
func disconnectAt(handshakeUnix int64, observedAt time.Time) time.Time {
	at := time.Unix(handshakeUnix, 0).UTC().Add(VPNOnlineWindow)
	if at.After(observedAt) {
		return observedAt
	}
	return at
}
