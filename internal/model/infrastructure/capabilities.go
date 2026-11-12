package infrastructure

import (
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Availability says how many of the laboratories that are used can do something. It never names a laboratory.
type Availability string

const (
	// AvailabilityAll: every laboratory that is used can.
	AvailabilityAll Availability = "all"
	// AvailabilitySome: some can and some cannot (or some have not reported yet and the rest can): a task that needs it
	// starts only on some laboratories.
	AvailabilitySome Availability = "some"
	// AvailabilityNone: none can; a task that needs it does not start anywhere.
	AvailabilityNone Availability = "none"
	// AvailabilityUnknown: nothing is known yet (no laboratory has reported its features).
	AvailabilityUnknown Availability = "unknown"
)

// LabCapabilities is what the laboratories that are used can run, for the catalog editor: no names, addresses,
// versions or counts. The key lists are fixed, so the editor can show a reason for every option it disables.
type LabCapabilities struct {
	// Known is false while no laboratory has reported; every option is then "unknown" (not "none").
	Known bool
	// Observations are the observation kinds a state check may use, by their document names, and the file keys "file.type"
	// and "file.checksum".
	Observations map[string]Availability
	// Access are the access kinds of a device that are served: http, terminal, vnc, rdp.
	Access map[string]Availability
	// Bootstrap: a device initialization (files, script) and the service command are supported.
	Bootstrap Availability
	// Checks: state checks are switched on.
	Checks Availability
	// VPNReverse: the block of connections the lab starts to VPN clients can be lifted (topology.vpn.reverse_connections).
	VPNReverse Availability
}

// CapabilityObservationKeys lists, in a fixed order, the keys of LabCapabilities.Observations.
func CapabilityObservationKeys() []string {
	keys := make([]string, 0, len(exerciseModel.ObservationKinds)+2)
	for _, kind := range exerciseModel.ObservationKinds {
		keys = append(keys, string(kind))
	}
	return append(keys, "file.type", "file.checksum")
}

// CapabilityAccessKeys lists the keys of LabCapabilities.Access.
func CapabilityAccessKeys() []string { return []string{"http", "terminal", "vnc", "rdp"} }

// CapabilitiesOf aggregates the features of the laboratories that are used: one entry per laboratory, nil for one that has not
// reported yet. Per option the answer is "all" when every reporting laboratory can, "none" when none can and "some"
// otherwise. A laboratory that has not reported yet may be able to: with one in the list, "all" becomes "some" and "none"
// becomes "unknown", so the editor warns instead of promising or forbidding what is not known. With no list or no report
// at all everything is "unknown".
func CapabilitiesOf(labs []*AgentFeatures) LabCapabilities {
	var reported []*AgentFeatures
	for _, features := range labs {
		if features != nil {
			reported = append(reported, features)
		}
	}
	out := LabCapabilities{
		Known: len(reported) > 0, Observations: map[string]Availability{}, Access: map[string]Availability{},
	}
	pending := len(reported) < len(labs)
	pick := func(can func(*AgentFeatures) bool) Availability {
		if len(reported) == 0 {
			return AvailabilityUnknown
		}
		yes := 0
		for _, features := range reported {
			if can(features) {
				yes++
			}
		}
		switch {
		case yes == len(reported) && !pending:
			return AvailabilityAll
		case yes == len(reported), yes > 0:
			return AvailabilitySome
		case pending:
			return AvailabilityUnknown
		default:
			return AvailabilityNone
		}
	}
	for _, key := range CapabilityObservationKeys() {
		out.Observations[key] = pick(func(f *AgentFeatures) bool { return f.Checks != nil && f.Checks.Enabled && f.Checks.Offers(key) })
	}
	served := map[string]func(*AgentFeatures) bool{
		"http":     func(*AgentFeatures) bool { return true },
		"terminal": func(f *AgentFeatures) bool { return f.Terminal != nil && f.Terminal.Enabled },
		"vnc":      func(*AgentFeatures) bool { return false },
		"rdp":      func(*AgentFeatures) bool { return false },
	}
	for _, key := range CapabilityAccessKeys() {
		out.Access[key] = pick(served[key])
	}
	out.Bootstrap = pick(func(f *AgentFeatures) bool { return f.Bootstrap != nil })
	out.Checks = pick(func(f *AgentFeatures) bool { return f.Checks != nil && f.Checks.Enabled })
	out.VPNReverse = pick(func(f *AgentFeatures) bool { return f.EnforcesVPNReverse() })
	return out
}
