package eventLabModel

import (
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestGroupSizingMismatchNeverGrantsReadinessAfterIdentityRecovery(t *testing.T) {
	need := Compute{CPUMillicores: 140, MemoryBytes: 576 << 20}
	g := NewGroup(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "group", need, testNow)
	g.AgentUID = "original"
	g.AgentGeneration = 1
	at := testNow.Add(time.Second)
	o := GroupObservation{Name: g.Name, UID: g.AgentUID, Generation: 1, ObservedAt: &at, DesiredState: "Running", InitialReady: true, Ready: true, ImmutableSizesKnown: true, VPNSize: Compute{125, 320 << 20}, GatewaySize: Compute{15, 32 << 20}}
	require.True(t, g.Observe(o, at))
	require.False(t, g.Ready)
	require.False(t, g.AllowsChildren(at))
	require.Equal(t, need, g.HeldCompute())
	g.Revision = 3
	g.OperationID = uuid.Must(uuid.NewV7())
	g.ObservedAt = nil
	o.OperationID = g.OperationID
	o.Revision = 3
	o.ObservedGeneration = 1
	o.ActualState = "Running"
	require.True(t, g.Observe(o, at))
	require.False(t, g.Ready)
	require.False(t, g.AllowsChildren(at))
	require.Equal(t, need, g.HeldCompute())
}
func TestGroupSizingMismatchWithoutBirthEvidenceStillRefusesIdentity(t *testing.T) {
	g := NewGroup(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "group", Compute{140, 576 << 20}, testNow)
	at := testNow.Add(time.Second)
	o := GroupObservation{Name: g.Name, UID: "arbitrary-same-name", Generation: 1, ObservedAt: &at, DesiredState: "Running", InitialReady: true, Ready: true, ImmutableSizesKnown: true, VPNSize: Compute{125, 320 << 20}, GatewaySize: Compute{15, 32 << 20}}
	before := g
	require.False(t, g.Observe(o, at))
	require.Equal(t, before, g)
	require.False(t, g.RecoverInitialIdentityForStop(o, nil, at))
	require.Equal(t, before, g)
}

func TestGroupCompatibleInitialIdentityWithoutReceiptPreservesNormalAdoption(t *testing.T) {
	g := NewGroup(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "group", Compute{140, 352 << 20}, testNow)
	at := testNow.Add(time.Second)
	o := GroupObservation{Name: g.Name, UID: "normal", Generation: 1, ObservedAt: &at, DesiredState: "Running", InitialReady: true, Ready: true, ImmutableSizesKnown: true, VPNSize: Compute{125, 320 << 20}, GatewaySize: Compute{15, 32 << 20}}
	require.True(t, g.Observe(o, at))
	require.Equal(t, "normal", g.AgentUID)
	require.True(t, g.Ready)
	require.True(t, g.AllowsChildren(at))
	require.Equal(t, Compute{140, 352 << 20}, g.HeldCompute())
}
