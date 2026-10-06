package infrastructure

import (
	"github.com/gofrs/uuid"

	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

// ResourcesView is what one lab (a team stand or a test lab) uses right now,
// summed over its devices; see labMonitoringModel.Resources.
type ResourcesView = labMonitoringModel.Resources

type standKey struct{ event, team uuid.UUID }

// standResources sums the resources per team stand over the current lab groups.
func standResources(current []labMonitoringModel.Current) map[standKey]ResourcesView {
	out := make(map[standKey]ResourcesView, len(current))
	for _, item := range current {
		key := standKey{item.EventID, item.EventTeamID}
		sum := out[key]
		sum.Add(labMonitoringModel.PayloadResources(item.Payload))
		out[key] = sum
	}
	return out
}

// LaunchView is the launch queue and image state of a stand; see labMonitoringModel.Launch.
type LaunchView = labMonitoringModel.Launch

// standLaunch folds the launch queue state per team stand over the current lab groups.
func standLaunch(current []labMonitoringModel.Current) map[standKey]LaunchView {
	out := make(map[standKey]LaunchView, len(current))
	for _, item := range current {
		key := standKey{item.EventID, item.EventTeamID}
		sum := out[key]
		sum.Add(labMonitoringModel.PayloadLaunch(item.Payload))
		out[key] = sum
	}
	return out
}
