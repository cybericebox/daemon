package infrastructure

import (
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"math"
)

// The versioned projection never infers flow/rate capacity from U/L/I/P alone.
type TrafficEnvelope struct {
	VPNRetainedFlows, GatewayRetainedFlows         int64
	VPNNewFlowsPerSecond, GatewayNewFlowsPerSecond int64
	VPNPacketsPerSecond, GatewayPacketsPerSecond   int64
	VPNPayloadMbps, GatewayPayloadMbps             float64
}
type GroupPodFormula struct {
	Base, PerUser, PerActiveLab, PerInternetLab, PerAllowedRelation, PerRetainedFlow, Floor, RoundTo resourcesModel.Amount
}
type GroupSizingProfile struct {
	ID, SupportState, ValidationProvenance, Scope string
	MaxInputs                                     GroupPlan
	VPN, Gateway                                  GroupPodFormula
}

func (g GroupSizes) Holds(need GroupSizes) bool {
	return need.VPN.Within(g.VPN) && need.Gateway.Within(g.Gateway)
}

func (e TrafficEnvelope) bounded() bool {
	return e.VPNRetainedFlows > 0 && e.GatewayRetainedFlows > 0 && e.VPNNewFlowsPerSecond > 0 && e.GatewayNewFlowsPerSecond > 0 && e.VPNPacketsPerSecond > 0 && e.GatewayPacketsPerSecond > 0 && e.VPNPayloadMbps > 0 && e.GatewayPayloadMbps > 0 && !math.IsInf(e.VPNPayloadMbps, 0) && !math.IsInf(e.GatewayPayloadMbps, 0) && !math.IsNaN(e.VPNPayloadMbps) && !math.IsNaN(e.GatewayPayloadMbps)
}
func (e TrafficEnvelope) within(m TrafficEnvelope) bool {
	return e.bounded() && m.bounded() && e.VPNRetainedFlows <= m.VPNRetainedFlows && e.GatewayRetainedFlows <= m.GatewayRetainedFlows && e.VPNNewFlowsPerSecond <= m.VPNNewFlowsPerSecond && e.GatewayNewFlowsPerSecond <= m.GatewayNewFlowsPerSecond && e.VPNPacketsPerSecond <= m.VPNPacketsPerSecond && e.GatewayPacketsPerSecond <= m.GatewayPacketsPerSecond && e.VPNPayloadMbps <= m.VPNPayloadMbps && e.GatewayPayloadMbps <= m.GatewayPayloadMbps
}
func (p GroupSizingProfile) Eligible(in GroupPlan) bool {
	m := p.MaxInputs
	return p.ID != "" && p.ID == in.ProfileID && p.SupportState == "SUPPORTED" && p.ValidationProvenance != "" && p.Scope != "" && in.MaxUsers > 0 && in.MaxActiveLabs >= 0 && in.InternetLabs >= 0 && in.AllowedRelations >= 0 && in.InternetLabs <= in.MaxActiveLabs && m.MaxUsers > 0 && m.MaxActiveLabs > 0 && m.InternetLabs >= 0 && m.AllowedRelations >= 0 && in.MaxUsers <= m.MaxUsers && in.MaxActiveLabs <= m.MaxActiveLabs && in.InternetLabs <= m.InternetLabs && in.AllowedRelations <= m.AllowedRelations && in.Envelope.within(m.Envelope) && p.VPN.valid() && p.Gateway.valid()
}
func (f GroupPodFormula) valid() bool {
	for _, a := range []resourcesModel.Amount{f.Base, f.PerUser, f.PerActiveLab, f.PerInternetLab, f.PerAllowedRelation, f.PerRetainedFlow, f.Floor, f.RoundTo} {
		if a.CPUMillicores < 0 || a.MemoryBytes < 0 {
			return false
		}
	}
	return f.Floor.CPUMillicores > 0 && f.Floor.MemoryBytes > 0 && f.RoundTo.CPUMillicores > 0 && f.RoundTo.MemoryBytes > 0
}
func (f GroupPodFormula) Size(p GroupPlan, flows int64) resourcesModel.Amount {
	sum := f.Base
	for _, term := range []struct {
		a resourcesModel.Amount
		n int64
	}{{f.PerUser, int64(p.MaxUsers)}, {f.PerActiveLab, int64(p.MaxActiveLabs)}, {f.PerInternetLab, int64(p.InternetLabs)}, {f.PerAllowedRelation, int64(p.AllowedRelations)}, {f.PerRetainedFlow, flows}} {
		add := func(a, b, n int64) int64 {
			if n > 0 && b > (math.MaxInt64-a)/n {
				return math.MaxInt64
			}
			return a + b*n
		}
		sum = resourcesModel.Amount{CPUMillicores: add(sum.CPUMillicores, term.a.CPUMillicores, term.n), MemoryBytes: add(sum.MemoryBytes, term.a.MemoryBytes, term.n)}
	}
	sum = sum.Max(f.Floor)
	round := func(v, step int64) int64 {
		if v > math.MaxInt64-step+1 {
			return math.MaxInt64
		}
		return ((v + step - 1) / step) * step
	}
	return resourcesModel.Amount{CPUMillicores: round(sum.CPUMillicores, f.RoundTo.CPUMillicores), MemoryBytes: round(sum.MemoryBytes, f.RoundTo.MemoryBytes)}
}
