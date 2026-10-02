package labagent

import (
	"context"
	"sync"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// FeatureCell holds the last features an agent reported. Copies of a Member share it, so a report
// reaches the fleet's readers at once. A nil cell holds nothing.
type FeatureCell struct {
	mu sync.RWMutex
	v  *infraModel.AgentFeatures
}

// NewFeatureCell returns a cell holding f (nil = nothing reported yet).
func NewFeatureCell(f *infraModel.AgentFeatures) *FeatureCell { return &FeatureCell{v: f} }

// Get returns the last reported features, nil before the first report.
func (c *FeatureCell) Get() *infraModel.AgentFeatures {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.v
}

// Set stores a report.
func (c *FeatureCell) Set(f infraModel.AgentFeatures) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.v = &f
	c.mu.Unlock()
}

// FeaturesOf converts the agent's report.
func FeaturesOf(r *labpb.FeaturesResponse) infraModel.AgentFeatures {
	p, ic, sch, ep, cert, proxy := r.GetStatePersistence(), r.GetImageCache(), r.GetScheduler(), r.GetEndpoints(), r.GetCertificate(), r.GetProxy()
	return infraModel.AgentFeatures{
		Persistence: infraModel.PersistenceFeature{
			Available: p.GetAvailable(), DefaultDebounce: p.GetDefaultDebounceMs(), WriteQuotaBytes: p.GetWriteQuotaBytes(),
			MaxFileSizeBytes: p.GetMaxFileSizeBytes(), ExcludedPaths: p.GetExcludedPaths(),
		},
		ImageCache:  infraModel.ImageCacheFeature{Enabled: ic.GetEnabled(), Registries: ic.GetRegistries()},
		Scheduler:   infraModel.SchedulerFeature{Enabled: sch.GetEnabled(), MaxPods: sch.GetMaxPods()},
		Endpoints:   infraModel.EndpointsFeature{LabsDomain: ep.GetLabsDomain(), VPNEndpoint: ep.GetVpnEndpoint()},
		Certificate: infraModel.CertificateFeature{NotAfterUnix: cert.GetNotAfterUnix(), IssuedTTLSeconds: cert.GetIssuedTtlSeconds()},
		Limits:      limitsOf(r),
		Proxy: infraModel.ProxyFeature{
			AccessTokenMaxTTLSeconds: proxy.GetAccessTokenMaxTtlSeconds(), SessionMaxTTLSeconds: proxy.GetSessionMaxTtlSeconds(), SessionIdleTTLSeconds: proxy.GetSessionIdleTtlSeconds(),
		},
	}
}

// limitsOf converts the agent's limits. The sizing of the group pods (VPN: base, per user, maximum;
// gateway: base, per internet lab, maximum) is read here once the laboratory protocol reports it; until then
// it stays unreported and adds nothing to a plan.
func limitsOf(r *labpb.FeaturesResponse) infraModel.LimitsFeature {
	dev, lab := r.GetLimits().GetDevice(), r.GetLimits().GetLab()
	vpn, gateway := groupSizingOf(r)
	return infraModel.LimitsFeature{
		DeviceMaxCPUMillicores: dev.GetMaxCpuMillicores(), DeviceMaxMemoryBytes: dev.GetMaxMemoryBytes(),
		LabMaxDevices: lab.GetMaxDevices(), TenantMaxLabs: r.GetLimits().GetTenant().GetMaxLabs(),
		VPN: vpn, Gateway: gateway, DeviceProfiles: r.GetDeviceProfiles(),
	}
}

// groupSizingOf is the seam for the laboratory's group pod sizing report.
func groupSizingOf(*labpb.FeaturesResponse) (vpn, gateway infraModel.GroupPodSizing) { return }

// wantsPersistence reports whether the topology asks any device to keep its state.
func wantsPersistence(topo exerciseModel.Topology) bool {
	for _, d := range topo.Devices {
		if d.Persistence != nil && d.Persistence.Enabled {
			return true
		}
	}
	return false
}

// PersistenceAvailable is true when at least one agent that takes new groups offers device state
// persistence. An agent that has not reported yet offers nothing.
func (f *Fleet) PersistenceAvailable() bool {
	for _, m := range f.Members() {
		if m.Enabled {
			if feat := m.Features.Get(); feat != nil && feat.Persistence.Available {
				return true
			}
		}
	}
	return false
}

// fitOf is the first limit of the member that the need passes; nil when it fits or the agent has not
// reported its limits (it refuses on its own if it must).
func fitOf(m *Member, need infraModel.PlacementNeed) *infraModel.FitViolation {
	feat := m.Features.Get()
	if feat == nil {
		return nil
	}
	return feat.Limits.Fits(need)
}

func noAgentFits(v *infraModel.FitViolation) error {
	e := infraModel.ErrNoAgentFitsTask
	if v == nil {
		return e.Err()
	}
	return e.WithContext("resource", v.Resource).WithContext("requested", v.Requested).WithContext("max", v.Max).Err()
}

// SetPolicy installs the platform's device resources settings: the frame an agent must meet and the
// presets a topology device resolves with. Before it is called the owner's defaults apply.
func (f *Fleet) SetPolicy(p resourcesModel.Policy) {
	f.mu.Lock()
	f.policy = p
	f.mu.Unlock()
}

// Policy is the device resources settings the fleet uses.
func (f *Fleet) Policy() resourcesModel.Policy {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.policy
}

// MeetsRequirements is false for an agent that reported maxima below the platform frame or fewer than 32
// devices per lab. An agent that has not reported is not held against: it refuses on its own if it must.
func (f *Fleet) MeetsRequirements(m *Member) bool {
	feat := m.Features.Get()
	return feat == nil || len(feat.Limits.UnmetRequirements(f.Policy().Frame)) == 0
}

// eligible is the enabled agents that meet the platform requirements, in priority order: the only ones used
// for planning and placement.
func (f *Fleet) eligible() []*Member {
	var out []*Member
	for _, m := range f.Members() {
		if m.Enabled && f.MeetsRequirements(m) {
			out = append(out, m)
		}
	}
	return out
}

// NeedFit says whether some agent that is used can run a need. A nil result means yes (or an agent has not
// reported); otherwise the violation with the largest limit among the agents, never an agent's name.
func (f *Fleet) NeedFit(need infraModel.PlacementNeed) *infraModel.FitViolation {
	var worst *infraModel.FitViolation
	for _, m := range f.eligible() {
		v := fitOf(m, need)
		if v == nil {
			return nil
		}
		if worst == nil || v.Max > worst.Max {
			worst = v
		}
	}
	return worst
}

// GroupSizes computes the sizes of a group's own pods for a plan with the formula of the agents that are
// used; with several agents the largest of each is taken, so the plan holds wherever the group lands.
// known is false when no agent has reported its sizing.
func (f *Fleet) GroupSizes(plan infraModel.GroupPlan) (sizes infraModel.GroupSizes, known bool) {
	for _, m := range f.eligible() {
		feat := m.Features.Get()
		if feat == nil || !(feat.Limits.VPN.Reported() || feat.Limits.Gateway.Reported()) {
			continue
		}
		s := feat.Limits.SizesFor(plan)
		sizes = infraModel.GroupSizes{VPN: sizes.VPN.Max(s.VPN), Gateway: sizes.Gateway.Max(s.Gateway)}
		known = true
	}
	return sizes, known
}

// labNeed is what one lab asks of an agent: its largest device and its container devices.
func labNeed(p resourcesModel.Policy, topo exerciseModel.Topology) infraModel.PlacementNeed {
	var need infraModel.PlacementNeed
	for _, d := range p.Devices(topo) {
		need.LabDevices++
		need.Device = need.Device.Max(d.Amount)
	}
	return need
}

// withLab adds one lab to what a group must hold: the largest device and the most devices of a lab; the
// plan of the group is kept.
func withLab(need, lab infraModel.PlacementNeed) infraModel.PlacementNeed {
	need.Device = need.Device.Max(lab.Device)
	need.LabDevices = max(need.LabDevices, lab.LabDevices)
	return need
}

// withSizes puts the sizes of the group's own pods, by the formula of the agent that takes the group, into
// the context of the call that creates it.
func (f *Fleet) withSizes(ctx context.Context, m *Member) context.Context {
	feat := m.Features.Get()
	if feat == nil {
		return ctx
	}
	return infraModel.WithGroupSizes(ctx, feat.Limits.SizesFor(infraModel.PlacementNeedFrom(ctx).Plan))
}

// setGroupSizes writes the planned pod sizes into a group to create. The laboratory protocol does not carry
// them yet (the laboratory agent adds the fields): this is the one place to wire them.
func setGroupSizes(*labpb.LabGroupItem, infraModel.GroupSizes) {}
