package eventAnalyticsModel

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeStandTransitions(t *testing.T) {
	team := uuid.UUID{15: 1}
	lab := uuid.UUID{15: 2}
	at := func(s int) time.Time { return time.Date(2026, 9, 29, 10, 0, s, 0, time.UTC) }
	log := []StandTransition{
		{TeamID: team, Source: StandSourceStand, Generation: 1, To: standCreating, At: at(0)},
		{TeamID: team, Source: StandSourceStand, Generation: 1, To: standFailed, Reason: "boom", At: at(30)},
		{TeamID: team, Source: StandSourceStand, Generation: 1, To: standCreating, At: at(40)},
		{TeamID: team, Source: StandSourceStand, Generation: 1, To: standReady, At: at(100)},
		{TeamID: team, Source: StandSourceLab, ChallengeID: &lab, ChallengeName: "Web", Generation: 1, To: labFailed, At: at(200)},
		{TeamID: team, Source: StandSourceLab, ChallengeID: &lab, Generation: 1, To: labFailed, At: at(210)},
	}
	h := AnalyzeStandTransitions(log)[team]

	require.NotNil(t, h.DeploySeconds)
	require.EqualValues(t, 100, *h.DeploySeconds)
	require.Equal(t, 1, h.Generations)
	require.Len(t, h.Failures, 2)

	seconds, ok := h.Failures[0].RecoverySeconds()
	require.True(t, ok)
	require.EqualValues(t, 70, seconds)
	require.Equal(t, "boom", h.Failures[0].Reason)

	_, ok = h.Failures[1].RecoverySeconds()
	require.False(t, ok, "a repeated failed status does not open a second failure")
}
