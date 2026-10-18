package imageRegistryModel

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	c0 = time.Date(2026, 10, 18, 6, 0, 0, 0, time.UTC)
	c1 = c0.Add(6 * time.Hour)
)

func TestCheckApplyTransitions(t *testing.T) {
	for name, tc := range map[string]struct {
		before  CheckState
		outcome Outcome
		want    Transition
		after   CheckState
	}{
		"first ok is silent":           {CheckUnknown, Working(), TransitionNone, CheckOK},
		"first refusal breaks":         {CheckUnknown, Refused(ReasonUnauthorized), TransitionBroke, CheckBroken},
		"ok stays ok":                  {CheckOK, Working(), TransitionNone, CheckOK},
		"ok to broken":                 {CheckOK, Refused(ReasonUnauthorized), TransitionBroke, CheckBroken},
		"broken stays broken, silent":  {CheckBroken, Refused(ReasonUnauthorized), TransitionNone, CheckBroken},
		"broken to ok restores":        {CheckBroken, Working(), TransitionRestored, CheckOK},
		"failed attempt keeps ok":      {CheckOK, Inconclusive(ReasonUnreachable), TransitionNone, CheckOK},
		"failed attempt keeps broken":  {CheckBroken, Inconclusive(ReasonRateLimited), TransitionNone, CheckBroken},
		"failed attempt keeps unknown": {CheckUnknown, Inconclusive(ReasonUnreachable), TransitionNone, CheckUnknown},
	} {
		t.Run(name, func(t *testing.T) {
			c := Check{State: tc.before}
			if tc.before != CheckUnknown {
				c.CheckedAt, c.Reason = c0, ReasonUnauthorized
			}
			require.Equal(t, tc.want, c.Apply(tc.outcome, c1))
			require.Equal(t, tc.after, c.State)
			require.Equal(t, c1, c.AttemptedAt)
		})
	}
}

func TestCheckApplyBookkeeping(t *testing.T) {
	c := Check{}
	c.Apply(Refused(ReasonUnauthorized), c0)
	require.Equal(t, Check{State: CheckBroken, CheckedAt: c0, Reason: ReasonUnauthorized, AttemptedAt: c0}, c)

	// a failed attempt keeps the verdict and its time, and remembers why the attempt failed
	c.Apply(Inconclusive(ReasonUnreachable), c1)
	require.Equal(t, Check{State: CheckBroken, CheckedAt: c0, Reason: ReasonUnauthorized, AttemptedAt: c1, Failure: ReasonUnreachable}, c)

	// a conclusive answer clears the failure and the reason
	c.Apply(Working(), c1)
	require.Equal(t, Check{State: CheckOK, CheckedAt: c1, AttemptedAt: c1}, c)
}

func TestCheckJSONRoundTrip(t *testing.T) {
	c := Check{State: CheckBroken, CheckedAt: c0, Reason: ReasonUnauthorized, AttemptedAt: c1, Failure: ReasonUnreachable}
	raw, err := c.Marshal()
	require.NoError(t, err)
	got, err := ParseCheck(raw)
	require.NoError(t, err)
	require.Equal(t, c, got)

	// empty and corrupt rows are forgiven: unknown state
	for _, in := range [][]byte{nil, []byte(`{}`), []byte(`not json`)} {
		got, _ := ParseCheck(in)
		require.Equal(t, Check{}, got)
	}
}
