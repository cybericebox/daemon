package labagent

import (
	"sync"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
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
	dev, lab, grp := r.GetLimits().GetDevice(), r.GetLimits().GetLab(), r.GetLimits().GetGroup()
	return infraModel.AgentFeatures{
		Persistence: infraModel.PersistenceFeature{
			Available: p.GetAvailable(), DefaultDebounce: p.GetDefaultDebounceMs(), WriteQuotaBytes: p.GetWriteQuotaBytes(),
			MaxFileSizeBytes: p.GetMaxFileSizeBytes(), ExcludedPaths: p.GetExcludedPaths(),
		},
		ImageCache:  infraModel.ImageCacheFeature{Enabled: ic.GetEnabled(), Registries: ic.GetRegistries()},
		Scheduler:   infraModel.SchedulerFeature{Enabled: sch.GetEnabled(), MaxPods: sch.GetMaxPods()},
		Endpoints:   infraModel.EndpointsFeature{LabsDomain: ep.GetLabsDomain(), VPNEndpoint: ep.GetVpnEndpoint()},
		Certificate: infraModel.CertificateFeature{NotAfterUnix: cert.GetNotAfterUnix(), IssuedTTLSeconds: cert.GetIssuedTtlSeconds()},
		Limits: infraModel.LimitsFeature{
			DeviceMaxCPUMillicores: dev.GetMaxCpuMillicores(), DeviceMaxMemoryBytes: dev.GetMaxMemoryBytes(),
			DeviceDefaultCPUMillicores: dev.GetDefaultCpuMillicores(), DeviceDefaultMemoryBytes: dev.GetDefaultMemoryBytes(),
			LabMaxDevices: lab.GetMaxDevices(), GroupMaxLabs: grp.GetMaxLabs(), GroupMaxCPUMillicores: grp.GetMaxCpuMillicores(),
			GroupMaxMemoryBytes: grp.GetMaxMemoryBytes(),
			TenantMaxLabs:       r.GetLimits().GetTenant().GetMaxLabs(),
		},
		Proxy: infraModel.ProxyFeature{
			AccessTokenMaxTTLSeconds: proxy.GetAccessTokenMaxTtlSeconds(), SessionMaxTTLSeconds: proxy.GetSessionMaxTtlSeconds(), SessionIdleTTLSeconds: proxy.GetSessionIdleTtlSeconds(),
		},
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

// withLab adds one lab to what a group must hold.
func withLab(need infraModel.PlacementNeed, lab infraModel.Demand) infraModel.PlacementNeed {
	return infraModel.PlacementNeed{Labs: append(append([]infraModel.Demand(nil), need.Labs...), lab)}
}

// fitOf is the first limit of the member that the labs pass; nil when they fit or the agent has not
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
	return e.WithContext("device", v.Device).WithContext("resource", v.Resource).
		WithContext("requested", v.Requested).WithContext("max", v.Max).Err()
}

// TopologyFit says how a topology sits on the enabled agents that reported their limits: it fits when at
// least one agent can run it (or one has not reported), and each agent that cannot is listed.
func (f *Fleet) TopologyFit(topo exerciseModel.Topology) infraModel.TopologyFit {
	need := infraModel.PlacementNeed{Labs: []infraModel.Demand{infraModel.DemandOf(topo)}}
	fit := infraModel.TopologyFit{FitsAny: true}
	reporters, fitting := 0, 0
	for _, m := range f.Members() {
		if !m.Enabled {
			continue
		}
		feat := m.Features.Get()
		if feat == nil {
			fitting++
			continue
		}
		reporters++
		if v := feat.Limits.Fits(need); v != nil {
			fit.Warnings = append(fit.Warnings, infraModel.FitWarning{Agent: m.Name, FitViolation: *v})
		} else {
			fitting++
		}
	}
	fit.FitsAny = fitting > 0 || reporters == 0
	return fit
}

// DeviceLimits is the most any enabled agent allows (for the editor's hints); known is false while no
// agent has reported its limits.
func (f *Fleet) DeviceLimits() (limits infraModel.LimitsFeature, known bool) {
	for _, m := range f.Members() {
		if !m.Enabled {
			continue
		}
		feat := m.Features.Get()
		if feat == nil {
			continue
		}
		l := feat.Limits
		if !known {
			limits, known = l, true
			continue
		}
		limits.DeviceMaxCPUMillicores = widest(limits.DeviceMaxCPUMillicores, l.DeviceMaxCPUMillicores)
		limits.DeviceMaxMemoryBytes = widest(limits.DeviceMaxMemoryBytes, l.DeviceMaxMemoryBytes)
		limits.DeviceDefaultCPUMillicores = max(limits.DeviceDefaultCPUMillicores, l.DeviceDefaultCPUMillicores)
		limits.DeviceDefaultMemoryBytes = max(limits.DeviceDefaultMemoryBytes, l.DeviceDefaultMemoryBytes)
		limits.LabMaxDevices = int32(widest(int64(limits.LabMaxDevices), int64(l.LabMaxDevices)))
		limits.GroupMaxLabs = int32(widest(int64(limits.GroupMaxLabs), int64(l.GroupMaxLabs)))
		limits.GroupMaxCPUMillicores = widest(limits.GroupMaxCPUMillicores, l.GroupMaxCPUMillicores)
		limits.GroupMaxMemoryBytes = widest(limits.GroupMaxMemoryBytes, l.GroupMaxMemoryBytes)
		limits.TenantMaxLabs = int32(widest(int64(limits.TenantMaxLabs), int64(l.TenantMaxLabs)))
	}
	return limits, known
}

// widest combines two caps where 0 means no limit: no limit is the widest.
func widest(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	return max(a, b)
}
