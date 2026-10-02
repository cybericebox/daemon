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
	return infraModel.AgentFeatures{
		Persistence: infraModel.PersistenceFeature{
			Available: p.GetAvailable(), DefaultDebounce: p.GetDefaultDebounceMs(), WriteQuotaBytes: p.GetWriteQuotaBytes(),
			MaxFileSizeBytes: p.GetMaxFileSizeBytes(), ExcludedPaths: p.GetExcludedPaths(),
		},
		ImageCache:  infraModel.ImageCacheFeature{Enabled: ic.GetEnabled(), Registries: ic.GetRegistries()},
		Scheduler:   infraModel.SchedulerFeature{Enabled: sch.GetEnabled(), MaxPods: sch.GetMaxPods()},
		Endpoints:   infraModel.EndpointsFeature{LabsDomain: ep.GetLabsDomain(), VPNEndpoint: ep.GetVpnEndpoint()},
		Certificate: infraModel.CertificateFeature{NotAfterUnix: cert.GetNotAfterUnix(), IssuedTTLSeconds: cert.GetIssuedTtlSeconds()},
		Proxy: infraModel.ProxyFeature{
			AccessTokenMaxTTLSeconds: proxy.GetAccessTokenMaxTtlSeconds(), SessionMaxTTLSeconds: proxy.GetSessionMaxTtlSeconds(),
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
