package exercise

import (
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// labCapabilitiesResponse tells the editor what the laboratories that are used can run, so it can disable an option with a
// reason instead of letting the author meet 73509 or 73512 at the start of a test lab. Every value is one of
// "all" (every laboratory can), "some" (some can, or some have not reported yet and the rest can: warn), "none" (no laboratory
// can: disable) and "unknown" (nothing is known yet: neither warn nor disable). It names no laboratory and carries no address,
// version or count. The keys of each map are fixed.
type labCapabilitiesResponse struct {
	// Known is false while no laboratory has reported its features: every value is then "unknown".
	Known bool `json:"Known"`
	// Observations: file, directory, process, port, kernel_param, request, permission, custom, file.type, file.checksum.
	Observations map[string]string `json:"Observations"`
	// Access: http, terminal, vnc, rdp. vnc and rdp are "none" (accepted by the schema, not served yet).
	Access map[string]string `json:"Access"`
	// Profiles: container, microvm, vm. The platform setting «Профілі вузлів»: "all" when ticked, else "none".
	Profiles map[string]string `json:"Profiles"`
	// Bootstrap: device initialization (files, script) and the service command.
	Bootstrap string `json:"Bootstrap"`
	// Checks: state checks are switched on.
	Checks string `json:"Checks"`
	// VPNReverse: reverse_connections of a VPN gateway can be enforced.
	VPNReverse string `json:"VPNReverse"`
}

func toLabCapabilities(v exerciseUseCase.LabCapabilitiesView) labCapabilitiesResponse {
	words := func(in map[string]infraModel.Availability) map[string]string {
		out := make(map[string]string, len(in))
		for key, value := range in {
			out[key] = string(value)
		}
		return out
	}
	return labCapabilitiesResponse{
		Known: v.Known, Observations: words(v.Observations), Access: words(v.Access), Profiles: words(v.Profiles),
		Bootstrap: string(v.Bootstrap), Checks: string(v.Checks), VPNReverse: string(v.VPNReverse),
	}
}
