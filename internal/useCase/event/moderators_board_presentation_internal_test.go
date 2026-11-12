package event

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModeratorsBoardHonoursTheEventPresentationSettings(t *testing.T) {
	board := func() []OwnChallengeView {
		return []OwnChallengeView{
			{
				Snapshot:     json.RawMessage(`{"name":"Web","difficulty":2}`),
				HintsEnabled: true,
				Hints:        []OwnHintView{{}},
			},
		}
	}

	shown := applyBoardPresentation(board(), true, false)
	require.JSONEq(t, `{"name":"Web","difficulty":2}`, string(shown[0].Snapshot))
	require.True(t, shown[0].HintsEnabled)
	require.Len(t, shown[0].Hints, 1)

	hidden := applyBoardPresentation(board(), false, true)
	require.JSONEq(
		t,
		`{"name":"Web"}`,
		string(hidden[0].Snapshot),
		"the difficulty is hidden like on the participants' board",
	)
	require.False(t, hidden[0].HintsEnabled)
	require.Empty(t, hidden[0].Hints)
}
