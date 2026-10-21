package eventself

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestPublicResultsResponseOmitsActivityTimestamps(t *testing.T) {
	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	view := eventUseCase.ResultsSnapshotView{Revision: 7, GeneratedAt: at,
		Scoreboard: []eventUseCase.ScoreboardEntryView{{TeamID: uuid.Must(uuid.NewV7()), TeamName: "Team", Points: 100, Solved: 1, LastSolveAt: &at}},
	}
	raw, err := json.Marshal(toResultsSnapshotResponse(view))
	require.NoError(t, err)
	var data map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &data))
	if _, present := data["GeneratedAt"]; present {
		t.Errorf("public results expose their global update time: %s", raw)
	}
	var entries []map[string]any
	require.NoError(t, json.Unmarshal(data["Scoreboard"], &entries))
	require.Len(t, entries, 1)
	require.NotContains(t, entries[0], "LastSolveAt")
	require.Equal(t, float64(100), entries[0]["Points"])
	require.Equal(t, float64(1), entries[0]["Solved"])
}
