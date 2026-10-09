package resourceCalendarUseCase

import (
	"encoding/json"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestResourceObservationWireKeepsHeldAndSubsetsSeparate(t *testing.T) {
	view := ObservationView(eventLabModel.ResourceTotals{Held: eventLabModel.Compute{CPUMillicores: 1125, MemoryBytes: 896 << 20}, PendingStarts: eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 << 20}, GroupServices: eventLabModel.Compute{CPUMillicores: 375, MemoryBytes: 384 << 20}, Storage: eventLabModel.StorageBudget{SnapshotQuotaBytes: 1 << 30, PhysicalStorageBytes: 42}})
	raw, err := json.Marshal(view)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Len(t, got, 7)
	require.Nil(t, got["ObservedAt"])
	require.Equal(t, false, got["Complete"])
	require.Equal(t, false, got["PhysicalStorageBytesAvailable"])
	require.Equal(t, "42", got["PhysicalStorageBytes"])
	require.Equal(t, map[string]any{"CPUMillicores": "1125", "MemoryBytes": "939524096", "SnapshotQuotaBytes": "1073741824"}, got["Held"])
	require.Equal(t, "750", got["PendingStarts"].(map[string]any)["CPUMillicores"])
	require.Equal(t, "375", got["GroupServices"].(map[string]any)["CPUMillicores"])
}
