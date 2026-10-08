package infrastructure

import (
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/stretchr/testify/require"
	"testing"
)

func profileFixture() GroupSizingProfile {
	return GroupSizingProfile{ID: "bounded", SupportState: "SUPPORTED", ValidationProvenance: "linux-test", Scope: "linux-vpn-gateway", MaxInputs: GroupPlan{MaxUsers: 10, MaxActiveLabs: 5, InternetLabs: 3, AllowedRelations: 20, Envelope: TrafficEnvelope{100, 100, 10, 10, 1000, 1000, 50, 50}}, VPN: GroupPodFormula{Base: resourcesModel.Amount{10, 1 << 20}, PerUser: resourcesModel.Amount{2, 1 << 20}, PerActiveLab: resourcesModel.Amount{3, 1 << 20}, PerInternetLab: resourcesModel.Amount{5, 1 << 20}, PerAllowedRelation: resourcesModel.Amount{7, 1 << 20}, PerRetainedFlow: resourcesModel.Amount{1, 1 << 10}, Floor: resourcesModel.Amount{1, 1}, RoundTo: resourcesModel.Amount{10, 1 << 20}}, Gateway: GroupPodFormula{Base: resourcesModel.Amount{20, 2 << 20}, PerInternetLab: resourcesModel.Amount{5, 1 << 20}, Floor: resourcesModel.Amount{1, 1}, RoundTo: resourcesModel.Amount{10, 1 << 20}}}
}
func TestSizingUsesPlannedULIP(t *testing.T) {
	p := profileFixture()
	l := LimitsFeature{SizingV2: []GroupSizingProfile{p}}
	plan := GroupPlan{MaxUsers: 2, MaxActiveLabs: 3, InternetLabs: 1, AllowedRelations: 4, ProfileID: p.ID, Envelope: TrafficEnvelope{5, 6, 1, 1, 100, 100, 10, 10}}
	got := l.SizesFor(plan)
	require.Equal(t, resourcesModel.Amount{70, 12 << 20}, got.VPN)
	require.Equal(t, resourcesModel.Amount{30, 3 << 20}, got.Gateway)
}
func TestUnvalidatedReducedProfileUsesSupportedFallback(t *testing.T) {
	l := LimitsFeature{VPN: GroupPodSizing{Base: resourcesModel.Amount{250, 256 << 20}}, Gateway: GroupPodSizing{Base: resourcesModel.Amount{125, 128 << 20}}}
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
	require.False(t, (GroupSizes{VPN: resourcesModel.Amount{50, 80 << 20}, Gateway: resourcesModel.Amount{25, 32 << 20}}).Holds(GroupSizes{VPN: resourcesModel.Amount{100, 128 << 20}, Gateway: resourcesModel.Amount{25, 32 << 20}}))
}
