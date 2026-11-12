package app

import (
	"sync"

	"github.com/gofrs/uuid"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// featureGapJournal is the part of the error journal the reporter writes to.
type featureGapJournal interface {
	ReportAgentFeatureGap(agent, feature string, reason infraModel.UnavailableReason)
}

// featureGapReporter writes a feature gap of an agent (a feature the cluster is configured for but its lab nodes do
// not confirm) to the error journal once: when it appears and whenever its code or node counts change, not at every
// report. A gap that is gone is forgotten, so it is reported again if it returns.
type featureGapReporter struct {
	mu   sync.Mutex
	seen map[gapKey]infraModel.UnavailableReason
}

type gapKey struct {
	agent   uuid.UUID
	feature string
}

func newFeatureGapReporter() *featureGapReporter {
	return &featureGapReporter{seen: map[gapKey]infraModel.UnavailableReason{}}
}

func (r *featureGapReporter) report(journal featureGapJournal, agent uuid.UUID, name string, f infraModel.AgentFeatures) {
	r.note(journal, agent, name, "persistence", f.PersistenceUnavailable)
	r.note(journal, agent, name, "lifecycle", f.LifecycleUnavailable)
}

func (r *featureGapReporter) note(journal featureGapJournal, agent uuid.UUID, name, feature string, reason *infraModel.UnavailableReason) {
	key := gapKey{agent, feature}
	r.mu.Lock()
	previous, known := r.seen[key]
	switch {
	case reason == nil:
		delete(r.seen, key)
		r.mu.Unlock()
		return
	case known && previous == *reason:
		r.mu.Unlock()
		return
	}
	r.seen[key] = *reason
	r.mu.Unlock()
	if journal != nil {
		journal.ReportAgentFeatureGap(name, feature, *reason)
	}
}
