package labagent

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

const (
	// FeaturesInterval is how often the features of an agent are read again: what the agent offers (persistence,
	// lifecycle) is live and can become available after the agent started.
	FeaturesInterval = time.Minute
	// featuresRefusalRefresh is the least time between two reads forced by a refusal, so a burst of refused labs
	// asks the agent once.
	featuresRefusalRefresh = 10 * time.Second
)

// PollFeatures reads the features of one agent every interval until ctx ends and hands each successful read to apply.
// A failed read keeps the last known features.
func PollFeatures(ctx context.Context, agent string, interval time.Duration, fetch func(context.Context) (*labpb.FeaturesResponse, error), apply func(context.Context, *labpb.FeaturesResponse) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		response, err := fetch(ctx)
		if err != nil {
			log.Debug().Err(err).Str("agent", agent).Msg("Features of the agent are not available")
			continue
		}
		if err = apply(ctx, response); err != nil {
			log.Warn().Err(err).Str("agent", agent).Msg("Failed to record the features of the agent")
		}
	}
}

// claimRefresh lets one caller per window read the features again because of a refusal.
func (c *FeatureCell) claimRefresh(now time.Time, window time.Duration) bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.refusalRefreshed.IsZero() && now.Sub(c.refusalRefreshed) < window {
		return false
	}
	c.refusalRefreshed = now
	return true
}

// refreshOnRefusal reads the features again when a lab was refused for what the cached ones say is missing (the agent
// may have it by now, e.g. persistence that became available after the start) and returns the features then in force.
// At most one read per window; a failed read leaves the cache as it is.
func refreshOnRefusal(ctx context.Context, cell *FeatureCell, now time.Time, fetch func(context.Context) (*labpb.FeaturesResponse, error)) *infraModel.AgentFeatures {
	if cell.claimRefresh(now, featuresRefusalRefresh) {
		if response, err := fetch(ctx); err == nil {
			cell.Set(FeaturesOf(response))
		} else {
			log.Debug().Err(err).Msg("Features of the agent could not be read again after a refusal")
		}
	}
	return cell.Get()
}
