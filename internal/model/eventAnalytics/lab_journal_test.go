package eventAnalyticsModel_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

var journalT0 = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)

func journalObs(team uuid.UUID, at time.Time, payload string) eventAnalyticsModel.LabObservation {
	return eventAnalyticsModel.LabObservation{
		ID: uuid.Must(uuid.NewV7()), EventTeamID: team, LabGroupName: "grp", ObservedAt: at,
		ReceivedAt: at.Add(700 * time.Millisecond), Payload: []byte(payload),
	}
}

func labPayload(ready bool, devReady bool, restarts int, dispatched, started int64) string {
	return fmt.Sprintf(`{"labs":[{"name":"web","status":{"ready":%t,"devices":[{"name":"app","ready":%t,"podPhase":"Running",`+
		`"restartCount":%d,"scheduling":{"state":"POD_STATE_STARTED","dispatchedUnixMs":"%d","startedUnixMs":"%d"}}]}}]}`,
		ready, devReady, restarts, dispatched, started)
}

func kinds(ts []eventAnalyticsModel.LabTransition) string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Kind)
	}
	return strings.Join(out, ",")
}

func TestLabJournal_LabAndDeviceTransitions(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	j := eventAnalyticsModel.NewLabJournal(nil, func() uuid.UUID { return uuid.Must(uuid.NewV7()) })
	disp, start := journalT0.Add(-30*time.Second).UnixMilli(), journalT0.Add(-10*time.Second).UnixMilli()

	// First sight of a not ready lab: only the pod scheduling facts.
	got := j.Apply(journalObs(team, journalT0, labPayload(false, false, 0, disp, 0)))
	require.Equal(t, "pod_scheduled", kinds(got))
	require.Equal(t, time.UnixMilli(disp).UTC(), got[0].At, "the agent's own time of the fact")
	require.Equal(t, journalT0.Add(700*time.Millisecond), got[0].ReceivedAt)
	require.Equal(t, "app", *got[0].DeviceName)

	// Nothing changed: nothing is written.
	require.Empty(t, j.Apply(journalObs(team, journalT0.Add(5*time.Second), labPayload(false, false, 0, disp, 0))))

	got = j.Apply(journalObs(team, journalT0.Add(10*time.Second), labPayload(true, true, 0, disp, start)))
	require.Equal(t, "lab_ready,device_ready,pod_started", kinds(got))
	require.Equal(t, journalT0.Add(10*time.Second), got[0].At)

	got = j.Apply(journalObs(team, journalT0.Add(20*time.Second), labPayload(true, true, 2, disp, start)))
	require.Equal(t, "device_restart", kinds(got))

	got = j.Apply(journalObs(team, journalT0.Add(30*time.Second), labPayload(false, true, 2, disp, start)))
	require.Equal(t, "lab_not_ready", kinds(got))

	// The states survive a restart of the job: a journal seeded from them continues.
	again := eventAnalyticsModel.NewLabJournal(j.Changed(), func() uuid.UUID { return uuid.Must(uuid.NewV7()) })
	require.Empty(t, again.Apply(journalObs(team, journalT0.Add(40*time.Second), labPayload(false, true, 2, disp, start))))
	require.Equal(t, "lab_ready", kinds(again.Apply(journalObs(team, journalT0.Add(50*time.Second), labPayload(true, true, 2, disp, start)))))
	require.NotEmpty(t, again.Changed())
	require.Empty(t, again.Changed(), "changed states are handed out once")
}

func clientsPayload(handshake time.Time, names ...string) string {
	var cl []string
	for _, n := range names {
		cl = append(cl, fmt.Sprintf(`{"name":%q,"status":{"statistics":{"lastHandshakeUnix":"%d","rxBytes":"1","txBytes":"1"}}}`, n, handshake.Unix()))
	}
	return `{"clients":[` + strings.Join(cl, ",") + `]}`
}

func TestLabJournal_ClientConnectAndDisconnect(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	j := eventAnalyticsModel.NewLabJournal(nil, func() uuid.UUID { return uuid.Must(uuid.NewV7()) })

	hs := journalT0.Add(-20 * time.Second)
	got := j.Apply(journalObs(team, journalT0, clientsPayload(hs, "p-a", "p-b")))
	require.Equal(t, "client_connected,client_connected", kinds(got))
	require.Equal(t, hs, got[0].At, "the connection started at the handshake")

	// p-a keeps handshaking; p-b is absent from the delta and its handshake ages out.
	got = j.Apply(journalObs(team, journalT0.Add(4*time.Minute), clientsPayload(journalT0.Add(3*time.Minute+50*time.Second), "p-a")))
	require.Equal(t, "client_disconnected", kinds(got))
	require.Equal(t, "p-b", *got[0].ClientName)
	require.Equal(t, hs.Add(eventAnalyticsModel.VPNOnlineWindow), got[0].At, "offline when the online window ended")

	// A client that is listed with a stale handshake disconnects too, once; a never connected one stays silent.
	stale := journalT0.Add(10 * time.Minute)
	got = j.Apply(journalObs(team, stale, clientsPayload(journalT0.Add(3*time.Minute+50*time.Second), "p-a")+""))
	require.Equal(t, "client_disconnected", kinds(got))
	require.Empty(t, j.Apply(journalObs(team, stale.Add(time.Minute), clientsPayload(time.Unix(0, 0), "p-z"))))
	require.Empty(t, j.Apply(journalObs(team, stale.Add(2*time.Minute), clientsPayload(journalT0.Add(3*time.Minute+50*time.Second), "p-a"))))
}

func TestLabJournal_VPNBootAndPolicy(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	j := eventAnalyticsModel.NewLabJournal(nil, func() uuid.UUID { return uuid.Must(uuid.NewV7()) })
	applied := journalT0.Add(-5 * time.Second).UnixMilli()
	payload := func(boot string, appliedAt int64) string {
		return fmt.Sprintf(`{"groups":[{"name":"grp","status":{"currentVpnBootId":%q,"pods":[{"name":"vpn","scheduling":{"startedUnixMs":"%d"}}]}}],`+
			`"policies":[{"policyUid":"pol-1","status":{"state":"Applied","appliedAtUnixMs":%d,"appliedRevision":"7"}}]}`, boot, applied, appliedAt)
	}
	got := j.Apply(journalObs(team, journalT0, payload("boot-1", applied)))
	require.Equal(t, "vpn_boot_changed,pod_started,policy_applied", kinds(got))
	require.Equal(t, "boot-1", *got[0].Detail)
	require.Equal(t, "vpn", *got[1].DeviceName)
	require.Equal(t, "revision 7", *got[2].Detail)
	require.Equal(t, time.UnixMilli(applied).UTC(), got[2].At)

	require.Empty(t, j.Apply(journalObs(team, journalT0.Add(time.Minute), payload("boot-1", applied))))
	got = j.Apply(journalObs(team, journalT0.Add(2*time.Minute), payload("boot-2", applied+60000)))
	require.Equal(t, "vpn_boot_changed,policy_applied", kinds(got))
	require.Empty(t, j.Apply(journalObs(team, journalT0.Add(3*time.Minute), `not json`)))
}

// The first look at an event that is over only sets the states: no transitions are made up.
func TestLabJournal_QuietRecordsStatesWithoutTransitions(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	j := eventAnalyticsModel.NewLabJournal(nil, func() uuid.UUID { return uuid.Must(uuid.NewV7()) })
	j.Quiet = true
	require.Empty(t, j.Apply(journalObs(team, journalT0, labPayload(true, true, 0, journalT0.UnixMilli(), journalT0.UnixMilli()))))
	require.NotEmpty(t, j.Changed(), "the states are remembered")

	j.Quiet = false
	require.Empty(t, j.Apply(journalObs(team, journalT0.Add(time.Minute), labPayload(true, true, 0, journalT0.UnixMilli(), journalT0.UnixMilli()))),
		"nothing changed since, so nothing is written once it is not quiet either")
}
