package labagent

import (
	"context"
	"sync"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
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
		TenantQuota: infraModel.TenantQuotaFeature{
			HasCPU: r.GetTenantQuota().GetHasCpuQuota(), CPUMillicores: r.GetTenantQuota().GetCpuQuotaMillicores(),
			HasMemory: r.GetTenantQuota().GetHasMemoryQuota(), MemoryBytes: r.GetTenantQuota().GetMemoryQuotaBytes(),
		},
		Proxy: infraModel.ProxyFeature{
			AccessTokenMaxTTLSeconds: proxy.GetAccessTokenMaxTtlSeconds(), SessionMaxTTLSeconds: proxy.GetSessionMaxTtlSeconds(), SessionIdleTTLSeconds: proxy.GetSessionIdleTtlSeconds(),
		},
	}
}

// MaxDeviceOf converts the largest device an agent says it can place; nil when it reports none. The agent never says more
// about its cluster than this one amount.
func MaxDeviceOf(c *labpb.CapacityResponse) *infraModel.AgentDevice {
	if !c.GetHasMaxDevice() {
		return nil
	}
	return &infraModel.AgentDevice{CPUMillicores: c.GetMaxDeviceCpuMillicores(), MemoryBytes: c.GetMaxDeviceMemoryBytes()}
}

// limitsOf converts the agent's limits and the sizing of its group pods (VPN: base, per user, maximum users;
// gateway: base, per internet lab, maximum labs); an agent that reports none adds nothing to a plan.
func limitsOf(r *labpb.FeaturesResponse) infraModel.LimitsFeature {
	dev, lab := r.GetLimits().GetDevice(), r.GetLimits().GetLab()
	pods := r.GetGroupPods()
	return infraModel.LimitsFeature{
		DeviceMaxCPUMillicores: dev.GetMaxCpuMillicores(), DeviceMaxMemoryBytes: dev.GetMaxMemoryBytes(),
		LabMaxDevices: lab.GetMaxDevices(), TenantMaxLabs: r.GetLimits().GetTenant().GetMaxLabs(),
		VPN: sizingOf(pods.GetVpn()), Gateway: sizingOf(pods.GetGateway()), DeviceProfiles: r.GetDeviceProfiles(),
		SizingV2:       sizingV2Of(pods.GetSizingV2()),
		DefaultVPN:     resourcesModel.Amount{CPUMillicores: pods.GetDefaultVpn().GetCpuMillicores(), MemoryBytes: pods.GetDefaultVpn().GetMemoryBytes()},
		DefaultGateway: resourcesModel.Amount{CPUMillicores: pods.GetDefaultGateway().GetCpuMillicores(), MemoryBytes: pods.GetDefaultGateway().GetMemoryBytes()},
	}
}

// sizingOf converts one pod sizing of the agent's group pods report.
func sizingOf(p *labpb.PodSizing) infraModel.GroupPodSizing {
	return infraModel.GroupPodSizing{
		Base:     resourcesModel.Amount{CPUMillicores: p.GetBaseCpuMillicores(), MemoryBytes: p.GetBaseMemoryBytes()},
		PerUnit:  resourcesModel.Amount{CPUMillicores: p.GetPerUnitCpuMillicores(), MemoryBytes: p.GetPerUnitMemoryBytes()},
		MaxUnits: p.GetMaxUnits(),
	}
}

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
func (f *Fleet) fitOf(m *Member, need infraModel.PlacementNeed) *infraModel.FitViolation {
	feat := m.Features.Get()
	if feat == nil {
		return nil
	}
	if v := feat.Limits.Fits(need); v != nil {
		return v
	}
	return f.groupPodsFit(feat.Limits, need.Plan)
}

// groupPodsFit refuses a plan whose group pods, rounded up to a preset size, would pass the largest preset or the
// agent's per-pod maximum (its sizing at the most units, rounded the same way): never an oversized group, never an
// unrounded one. The violation names the memory.
func (f *Fleet) groupPodsFit(l infraModel.LimitsFeature, plan infraModel.GroupPlan) *infraModel.FitViolation {
	policy := f.Policy()
	for _, pod := range []struct {
		sizing infraModel.GroupPodSizing
		units  int
	}{{l.VPN, plan.MaxUsers}, {l.Gateway, plan.InternetLabs}} {
		if !pod.sizing.Reported() {
			continue
		}
		size := pod.sizing.Size(pod.units)
		rounded, ok := policy.RoundUpWithin(size)
		if !ok {
			return &infraModel.FitViolation{Resource: infraModel.FitMemory, Requested: size.MemoryBytes, Max: policy.LargestPreset().MemoryBytes}
		}
		if pod.sizing.MaxUnits > 0 {
			if podMax, fits := policy.RoundUpWithin(pod.sizing.Size(int(pod.sizing.MaxUnits))); fits && !rounded.Within(podMax) {
				return &infraModel.FitViolation{Resource: infraModel.FitMemory, Requested: rounded.MemoryBytes, Max: podMax.MemoryBytes}
			}
		}
	}
	return nil
}

func noAgentFits(v *infraModel.FitViolation) error {
	e := infraModel.ErrNoAgentFitsTask
	if v == nil {
		return e.Err()
	}
	return e.WithPublicContext("resource", v.Resource).WithPublicContext("requested", v.Requested).WithPublicContext("max", v.Max).Err()
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
		v := f.fitOf(m, need)
		if v == nil {
			return nil
		}
		if worst == nil || v.Max > worst.Max {
			worst = v
		}
	}
	return worst
}

// roundedSizes is the group's pod sizes rounded up to whole blocks: what is reserved and sent to the agent.
func (f *Fleet) roundedSizes(s infraModel.GroupSizes) infraModel.GroupSizes {
	policy := f.Policy()
	return infraModel.GroupSizes{VPN: policy.RoundUp(s.VPN), Gateway: policy.RoundUp(s.Gateway)}
}

// GroupSizes computes the sizes of a group's own pods for a plan with the formula of the agents that are
// used, rounded up to whole blocks; with several agents the largest of each is taken, so the plan holds
// wherever the group lands. known is false when no agent has reported its sizing.
func (f *Fleet) GroupSizes(plan infraModel.GroupPlan) (sizes infraModel.GroupSizes, known bool) {
	for _, m := range f.eligible() {
		feat := m.Features.Get()
		if feat == nil || !(feat.Limits.VPN.Reported() || feat.Limits.Gateway.Reported() || feat.Limits.DefaultVPN != (resourcesModel.Amount{}) || feat.Limits.DefaultGateway != (resourcesModel.Amount{}) || len(feat.Limits.SizingV2) > 0) {
			continue
		}
		s := feat.Limits.SizesFor(plan)
		sizes = infraModel.GroupSizes{VPN: sizes.VPN.Max(s.VPN), Gateway: sizes.Gateway.Max(s.Gateway)}
		known = true
	}
	return f.roundedSizes(sizes), known
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
	sizes, known := f.GroupSizes(infraModel.PlacementNeedFrom(ctx).Plan)
	if !known {
		return ctx
	}
	return infraModel.WithGroupSizes(ctx, sizes)
}

// setGroupSizes writes the planned pod sizes into a group to create, explicitly; the agent rejects a size over
// its maximum.
func setGroupSizes(item *labpb.LabGroupItem, sizes infraModel.GroupSizes) {
	if sizes.VPN.CPUMillicores > 0 && sizes.VPN.MemoryBytes > 0 {
		item.VpnSize = &labpb.PodSize{CpuMillicores: sizes.VPN.CPUMillicores, MemoryBytes: sizes.VPN.MemoryBytes}
	}
	if sizes.Gateway.CPUMillicores > 0 && sizes.Gateway.MemoryBytes > 0 {
		item.GatewaySize = &labpb.PodSize{CpuMillicores: sizes.Gateway.CPUMillicores, MemoryBytes: sizes.Gateway.MemoryBytes}
	}
}

// SchedulerMaxPods is the pods the agents that are used start at once, summed; 0 when some agent has no limit or
// none has reported (the launch then needs one wave).
func (f *Fleet) SchedulerMaxPods() int {
	total := 0
	for _, m := range f.eligible() {
		feat := m.Features.Get()
		if feat == nil || !feat.Scheduler.Enabled || feat.Scheduler.MaxPods <= 0 {
			return 0
		}
		total += int(feat.Scheduler.MaxPods)
	}
	return total
}

// RequireLabLifecyclePreparation checks the actual selected producer; ordinary
// persistence support is not a required capture barrier.
func (c *Client) RequireLabLifecyclePreparation(ctx context.Context, _ string, policy eventLabModel.Policy, topology exerciseModel.Topology) error {
	if err := policy.ValidatePreparation(topology, true); err != nil {
		return err
	}
	if policy.SnapshotMode != "required" {
		return nil
	}
	feature, err := c.lifecycleFeature(ctx)
	if err != nil {
		return err
	}
	return policy.ValidatePreparation(topology, feature.GetPerLabStop() && feature.GetRequiredSnapshot() && feature.GetConfirmedRuntime())
}
func (c *Client) lifecycleFeature(ctx context.Context) (*labpb.LifecycleFeature, error) {
	response, err := c.GetFeatures(ctx, &labpb.Empty{})
	if err != nil {
		return nil, agentErr("get lab lifecycle features", err)
	}
	return response.GetLifecycle(), nil
}

func (f *Fleet) RequireLabLifecyclePreparation(ctx context.Context, group string, policy eventLabModel.Policy, topology exerciseModel.Topology) error {
	if err := policy.ValidatePreparation(topology, true); err != nil {
		return err
	}
	if policy.SnapshotMode != "required" {
		return nil
	}
	ctx = infraModel.WithPlacementNeed(ctx, withLab(infraModel.PlacementNeedFrom(ctx), labNeed(f.Policy(), topology)))
	m, err := f.memberForCreate(ctx, group)
	if err != nil {
		return err
	}
	return m.Client.RequireLabLifecyclePreparation(ctx, group, policy, topology)
}

func sizingV2Of(v *labpb.GroupPodsSizingV2) []infraModel.GroupSizingProfile {
	if len(v.GetProfiles()) == 0 {
		return nil
	}
	amount := func(p *labpb.PodSize) resourcesModel.Amount {
		return resourcesModel.Amount{CPUMillicores: p.GetCpuMillicores(), MemoryBytes: p.GetMemoryBytes()}
	}
	formula := func(f *labpb.GroupPodFormula) infraModel.GroupPodFormula {
		return infraModel.GroupPodFormula{Base: amount(f.GetBase()), PerUser: amount(f.GetPerUser()), PerActiveLab: amount(f.GetPerActiveLab()), PerInternetLab: amount(f.GetPerInternetLab()), PerAllowedRelation: amount(f.GetPerAllowedRelation()), PerRetainedFlow: amount(f.GetPerRetainedFlow()), Floor: amount(f.GetFloor()), RoundTo: amount(f.GetRoundTo())}
	}
	out := make([]infraModel.GroupSizingProfile, 0, len(v.GetProfiles()))
	for _, p := range v.GetProfiles() {
		in := p.GetMaxInputs()
		e := in.GetEnvelope()
		out = append(out, infraModel.GroupSizingProfile{ID: p.GetId(), SupportState: p.GetSupportState(), ValidationProvenance: p.GetValidationProvenance(), Scope: p.GetScope(), MaxInputs: infraModel.GroupPlan{MaxUsers: int(in.GetMaxUsers()), MaxActiveLabs: int(in.GetMaxActiveLabs()), InternetLabs: int(in.GetInternetLabs()), AllowedRelations: int(in.GetAllowedRelations()), Envelope: infraModel.TrafficEnvelope{VPNRetainedFlows: e.GetVpnRetainedFlows(), GatewayRetainedFlows: e.GetGatewayRetainedFlows(), VPNNewFlowsPerSecond: e.GetVpnNewFlowsPerSecond(), GatewayNewFlowsPerSecond: e.GetGatewayNewFlowsPerSecond(), VPNPacketsPerSecond: e.GetVpnPacketsPerSecond(), GatewayPacketsPerSecond: e.GetGatewayPacketsPerSecond(), VPNPayloadMbps: e.GetVpnPayloadMbps(), GatewayPayloadMbps: e.GetGatewayPayloadMbps()}}, VPN: formula(p.GetVpn()), Gateway: formula(p.GetGateway())})
	}
	return out
}

// SnapshotQuotaFor reserves the configured per-device write quota; absent quota
// is unknown and cannot authorize persistent generation creation.
func (f *Fleet) SnapshotQuotaFor(t exerciseModel.Topology) (int64, bool) {
	count := int64(0)
	for _, d := range t.Devices {
		if d.Persistence != nil && d.Persistence.Enabled {
			count++
		}
	}
	if count == 0 {
		return 0, true
	}
	var quota int64
	known := false
	for _, m := range f.eligible() {
		feat := m.Features.Get()
		if feat == nil || !feat.Persistence.Available || feat.Persistence.WriteQuotaBytes <= 0 {
			return 0, false
		}
		quota = max(quota, feat.Persistence.WriteQuotaBytes)
		known = true
	}
	if quota > 0 && count > (1<<63-1)/quota {
		return 0, false
	}
	return count * quota, known
}
