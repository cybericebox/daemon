package labagent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

func persistenceReport(available bool) *labpb.FeaturesResponse {
	return &labpb.FeaturesResponse{StatePersistence: &labpb.StatePersistenceFeature{Available: available}}
}

// A refusal for missing persistence reads the features again: persistence that became available after the agent
// started is seen at once, and a burst of refusals asks the agent once.
func TestRefreshOnRefusalSeesLatePersistence(t *testing.T) {
	cell := NewFeatureCell(&infraModel.AgentFeatures{})
	now := time.Now()
	var reads atomic.Int32
	fetch := func(context.Context) (*labpb.FeaturesResponse, error) {
		reads.Add(1)
		return persistenceReport(true), nil
	}

	got := refreshOnRefusal(context.Background(), cell, now, fetch)
	require.NotNil(t, got)
	assert.True(t, got.Persistence.Available, "the cell holds the new report")
	assert.EqualValues(t, 1, reads.Load())

	cell.Set(infraModel.AgentFeatures{})
	got = refreshOnRefusal(context.Background(), cell, now.Add(time.Second), fetch)
	assert.False(t, got.Persistence.Available, "inside the window the agent is not asked again")
	assert.EqualValues(t, 1, reads.Load())

	got = refreshOnRefusal(context.Background(), cell, now.Add(featuresRefusalRefresh+time.Second), fetch)
	assert.True(t, got.Persistence.Available, "after the window it is")
	assert.EqualValues(t, 2, reads.Load())
}

// A failed read leaves the cached features as they were.
func TestRefreshOnRefusalKeepsTheCacheOnAFailedRead(t *testing.T) {
	cell := NewFeatureCell(&infraModel.AgentFeatures{})
	got := refreshOnRefusal(context.Background(), cell, time.Now(), func(context.Context) (*labpb.FeaturesResponse, error) {
		return nil, errors.New("agent offline")
	})
	require.NotNil(t, got)
	assert.False(t, got.Persistence.Available)
}

// The poll hands every successful read to the sink and skips a failed one.
func TestPollFeaturesAppliesEachRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var reads, applied atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		PollFeatures(ctx, "a", 5*time.Millisecond,
			func(context.Context) (*labpb.FeaturesResponse, error) {
				if reads.Add(1) == 1 {
					return nil, errors.New("offline")
				}
				return persistenceReport(true), nil
			},
			func(context.Context, *labpb.FeaturesResponse) error {
				if applied.Add(1) >= 2 {
					cancel()
				}
				return nil
			})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the poll did not stop")
	}
	assert.GreaterOrEqual(t, reads.Load(), int32(3), "the failed read did not stop the poll")
	assert.EqualValues(t, 2, applied.Load())
}
