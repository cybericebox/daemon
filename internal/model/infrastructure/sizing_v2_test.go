package infrastructure

import (
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/stretchr/testify/require"
	"testing"
)

func profileFixture() GroupSizingProfile {
	return GroupSizingProfile{ID: "bounded", SupportState: "SUPPORTED", ValidationProvenance: "linux-test", Scope: "linux-vpn-gateway", MaxInputs: GroupPlan{MaxUsers: 10, MaxActiveLabs: 5, InternetLabs: 3, AllowedRelations: 20, Envelope: TrafficEnvelope{100, 100, 10, 10, 1000, 1000, 50, 50}}, VPN: GroupPodFormula{Base: resourcesModel.Amount{CPUMillicores: 10, MemoryBytes: 1 << 20}, PerUser: resourcesModel.Amount{CPUMillicores: 2, MemoryBytes: 1 << 20}, PerActiveLab: resourcesModel.Amount{CPUMillicores: 3, MemoryBytes: 1 << 20}, PerInternetLab: resourcesModel.Amount{CPUMillicores: 5, MemoryBytes: 1 << 20}, PerAllowedRelation: resourcesModel.Amount{CPUMillicores: 7, MemoryBytes: 1 << 20}, PerRetainedFlow: resourcesModel.Amount{CPUMillicores: 1, MemoryBytes: 1 << 10}, Floor: resourcesModel.Amount{CPUMillicores: 1, MemoryBytes: 1}, RoundTo: resourcesModel.Amount{CPUMillicores: 10, MemoryBytes: 1 << 20}}, Gateway: GroupPodFormula{Base: resourcesModel.Amount{CPUMillicores: 20, MemoryBytes: 2 << 20}, PerInternetLab: resourcesModel.Amount{CPUMillicores: 5, MemoryBytes: 1 << 20}, Floor: resourcesModel.Amount{CPUMillicores: 1, MemoryBytes: 1}, RoundTo: resourcesModel.Amount{CPUMillicores: 10, MemoryBytes: 1 << 20}}}
}
func TestSizingUsesPlannedULIP(t *testing.T) {
	p := profileFixture()
	l := LimitsFeature{SizingV2: []GroupSizingProfile{p}}
	plan := GroupPlan{MaxUsers: 2, MaxActiveLabs: 3, InternetLabs: 1, AllowedRelations: 4, ProfileID: p.ID, Envelope: TrafficEnvelope{5, 6, 1, 1, 100, 100, 10, 10}}
	got := l.SizesFor(plan)
	require.Equal(t, resourcesModel.Amount{CPUMillicores: 70, MemoryBytes: 12 << 20}, got.VPN)
	require.Equal(t, resourcesModel.Amount{CPUMillicores: 30, MemoryBytes: 3 << 20}, got.Gateway)
}
func TestUnvalidatedReducedProfileUsesSupportedFallback(t *testing.T) {
	l := LimitsFeature{VPN: GroupPodSizing{Base: resourcesModel.Amount{CPUMillicores: 250, MemoryBytes: 256 << 20}}, Gateway: GroupPodSizing{Base: resourcesModel.Amount{CPUMillicores: 125, MemoryBytes: 128 << 20}}}
	plan := GroupPlan{MaxUsers: 2, MaxActiveLabs: 1, InternetLabs: 1, AllowedRelations: 1, ProfileID: "bounded", Envelope: TrafficEnvelope{5, 5, 1, 1, 100, 100, 10, 10}}
	fallback := l.SizesFor(plan)
	for name, change := range map[string]func(*GroupSizingProfile){"candidate": func(p *GroupSizingProfile) { p.SupportState = "CANDIDATE" }, "no validation": func(p *GroupSizingProfile) { p.ValidationProvenance = "" }, "no envelope": func(p *GroupSizingProfile) { p.MaxInputs.Envelope = TrafficEnvelope{} }, "outside rate": func(p *GroupSizingProfile) { p.MaxInputs.Envelope.VPNPacketsPerSecond = 1 }, "unknown support": func(p *GroupSizingProfile) { p.SupportState = "supported" }} {
		t.Run(name, func(t *testing.T) {
			p := profileFixture()
			change(&p)
			l.SizingV2 = []GroupSizingProfile{p}
			require.Equal(t, fallback, l.SizesFor(plan))
		})
	}
}
func TestCreatedGroupCannotBeSilentlyUndersized(t *testing.T) {
	require.False(t, (GroupSizes{VPN: resourcesModel.Amount{CPUMillicores: 50, MemoryBytes: 80 << 20}, Gateway: resourcesModel.Amount{CPUMillicores: 25, MemoryBytes: 32 << 20}}).Holds(GroupSizes{VPN: resourcesModel.Amount{CPUMillicores: 100, MemoryBytes: 128 << 20}, Gateway: resourcesModel.Amount{CPUMillicores: 25, MemoryBytes: 32 << 20}}))
}
