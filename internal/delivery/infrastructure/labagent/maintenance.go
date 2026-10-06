package labagent

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// MaintenanceInterval is how often an agent's maintenance windows are read: the agent has no change feed.
const MaintenanceInterval = time.Minute

// WindowsOf converts the maintenance windows an agent reported (the ones that apply to the platform's tenant).
func WindowsOf(l *labpb.MaintenanceWindowList) []infraModel.AgentMaintenanceWindow {
	out := make([]infraModel.AgentMaintenanceWindow, 0, len(l.GetItems()))
	for _, w := range l.GetItems() {
		item := infraModel.AgentMaintenanceWindow{
			Name: w.GetName(), Reason: w.GetReason(), From: time.UnixMilli(w.GetFromUnixMs()).UTC(), AllTenants: w.GetAllTenants(),
			HasCapacity: w.GetHasCapacity(), CPUMillicores: w.GetCapacityCpuMillicores(), MemoryBytes: w.GetCapacityMemoryBytes(),
		}
		if w.GetToUnixMs() != 0 {
			to := time.UnixMilli(w.GetToUnixMs()).UTC()
			item.To = &to
		}
		out = append(out, item)
	}
	return out
}

// PollMaintenance reads the maintenance windows of one agent every interval until ctx ends and hands each successful
// read to record. A failed read (the agent is offline, it is an older one, or its CRD is not installed) records
// nothing: the last known windows stay.
func PollMaintenance(ctx context.Context, agent string, interval time.Duration, fetch func(context.Context) (*labpb.MaintenanceWindowList, error), record func(context.Context, []infraModel.AgentMaintenanceWindow) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if list, err := fetch(ctx); err != nil {
			log.Debug().Err(err).Str("agent", agent).Msg("Maintenance windows of the agent are not available")
		} else if err = record(ctx, WindowsOf(list)); err != nil {
			log.Warn().Err(err).Str("agent", agent).Msg("Failed to record the maintenance windows of the agent")
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
