package lab

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/errorJournal"
)

func TestThrottleJournalWritesOneGroupedEntryPerDevicePerWindow(t *testing.T) {
	var got []errorJournal.Event
	clock := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	notify := ThrottleJournal(func(e errorJournal.Event) { got = append(got, e) }, func() time.Time { return clock })
	alert := ThrottleAlert{AgentID: "a1", LabName: "lab-1", LabGroupName: eventGroup, Run: "run-1", Device: "web", Percent: 80, WindowSeconds: 300}

	require.NoError(t, notify(context.Background(), alert))
	require.Len(t, got, 1)
	require.Equal(t, errorJournal.NotifyNever, got[0].Notify, "records only, nobody is messaged")
	require.Equal(t, errorJournal.KindLabComponent, got[0].Kind)

	// The same device in another lab run (a restart) within the window is the same entry and is not repeated.
	alert.Run = "run-2"
	require.NoError(t, notify(context.Background(), alert))
	require.Len(t, got, 1)

	// Another device is another entry.
	other := alert
	other.Device = "db"
	require.NoError(t, notify(context.Background(), other))
	require.Len(t, got, 2)
	require.NotEqual(t, got[0].Key, got[1].Key)

	// After the window the device is written again under the same key, so the group's counter grows.
	clock = clock.Add(ThrottleJournalWindow)
	require.NoError(t, notify(context.Background(), alert))
	require.Len(t, got, 3)
	require.Equal(t, got[0].Key, got[2].Key)
}
