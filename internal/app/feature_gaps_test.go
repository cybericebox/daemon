package app

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

type gapRecorder struct{ reported []string }

func (r *gapRecorder) ReportAgentFeatureGap(agent, feature string, reason infraModel.UnavailableReason) {
	r.reported = append(r.reported, agent+" "+feature+" "+reason.Code)
}

// A gap is written once when it appears and again only when it changes or comes back; a report without one writes nothing.
func TestFeatureGapReporter(t *testing.T) {
	journal := &gapRecorder{}
	reporter := newFeatureGapReporter()
	agent := uuid.Must(uuid.NewV7())
	gap := &infraModel.UnavailableReason{Code: "CONTAINERD_ROOT_MISSING", NodesFailed: 1, NodesAsked: 2}

	reporter.report(journal, agent, "kind", infraModel.AgentFeatures{})
	assert.Empty(t, journal.reported)

	features := infraModel.AgentFeatures{PersistenceUnavailable: gap}
	reporter.report(journal, agent, "kind", features)
	reporter.report(journal, agent, "kind", features)
	assert.Equal(t, []string{"kind persistence CONTAINERD_ROOT_MISSING"}, journal.reported, "the same gap twice is one entry")

	features.PersistenceUnavailable = &infraModel.UnavailableReason{Code: "CONTAINERD_ROOT_MISSING", NodesFailed: 2, NodesAsked: 2}
	features.LifecycleUnavailable = &infraModel.UnavailableReason{Code: "NODE_UNCONFIRMED", NodesFailed: 1, NodesAsked: 2}
	reporter.report(journal, agent, "kind", features)
	assert.Len(t, journal.reported, 3, "a changed count and a new feature are written")

	reporter.report(journal, agent, "kind", infraModel.AgentFeatures{})
	reporter.report(journal, agent, "kind", infraModel.AgentFeatures{PersistenceUnavailable: gap})
	assert.Len(t, journal.reported, 4, "a gap that came back is written again")
}
