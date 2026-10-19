package event

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// The template previews of an event show its own data: name, tag, site links in the real format (the same as
// dispatch builds) and its times. Only what cannot exist yet stays a clear placeholder.
func TestEventPreviewValues_UseTheEventsOwnData(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	start := time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC)
	finish := start.Add(8 * time.Hour)
	e := eventModel.Event{ID: id, Tag: "khctf", Name: "Kharkiv Regional CTF 2026", Lifecycle: eventModel.Lifecycle{StartAt: start, FinishAt: &finish}}

	values := eventPreviewValues(e, "cybericebox.com")

	require.Equal(t, "Kharkiv Regional CTF 2026", values["event_name"])
	require.Equal(t, "khctf", values["event_tag"])
	require.Equal(t, id.String(), values["scope_event_id"])
	require.Equal(t, "https://khctf.cybericebox.com/", values["event_url"])
	require.Equal(t, "https://khctf.cybericebox.com/invite", values["invite_url"])
	require.Equal(t, "https://khctf.cybericebox.com/participation?tab=team", values["team_url"])
	require.Equal(t, "20.10.2026 10:00", values["start_at"])
	require.Equal(t, "20.10.2026 18:00", values["finish_at"])
	require.Equal(t, "Імʼя учасника", values["user_name"])
	require.Equal(t, "Назва команди", values["team_name"])
	for name, value := range values {
		require.NotContains(t, value, "example.org", name)
		require.NotContains(t, value, "Cyber ICE Box CTF", name)
	}
}

func TestEventPreviewValues_NoFinishLeavesTheFinishOut(t *testing.T) {
	e := eventModel.Event{ID: uuid.Must(uuid.NewV7()), Tag: "open", Name: "Open", Lifecycle: eventModel.Lifecycle{StartAt: time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC)}}
	_, ok := eventPreviewValues(e, "cybericebox.com")["finish_at"]
	require.False(t, ok)
}
