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

func archiveOf(t *testing.T, lines ...string) []byte {
	t.Helper()
	data, err := eventAnalyticsModel.CompressLabArchive(lines)
	require.NoError(t, err)
	return data
}

func linesOf(t *testing.T, data []byte) string {
	t.Helper()
	var all []string
	require.NoError(t, eventAnalyticsModel.LabArchiveLines(eventAnalyticsModel.LabArchiveCodecZstdNDJSON, data, func(l []byte) error {
		all = append(all, string(l))
		return nil
	}))
	return strings.Join(all, "\n")
}

var (
	scrubTeam = uuid.Must(uuid.NewV7())
	scrubT0   = time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
)

func scrubRow(at time.Time, payload string) string {
	return fmt.Sprintf(`{"event_team_id":%q,"lab_group_name":"grp","observed_at":%q,"payload":%s}`,
		scrubTeam, at.Format(time.RFC3339), payload)
}

// The windows of a client's IP are found in an archive alone; scrubbing empties the IP (with or without /32)
// wherever it is inside a window, also in a row that names no client; a row that gives the address to another client
// and a row outside the window stay. Text that merely mentions the address stays too.
func TestScrubLabArchive_BlanksTheIPInsideItsWindowOnly(t *testing.T) {
	codec := eventAnalyticsModel.LabArchiveCodecZstdNDJSON
	own := scrubRow(scrubT0, `{"clients":[{"name":"p-a","status":{"assignedIp":"10.8.0.7"}},{"name":"p-b","status":{"assignedIp":"10.8.0.9"}}],`+
		`"policies":[{"allowedIps":["10.8.0.7/32","10.8.0.9"],"note":"gw 10.8.0.7"}]}`)
	delta := scrubRow(scrubT0.Add(time.Minute), `{"policies":[{"allowedIps":["10.8.0.7/32"]}],"groups":[{"vpn":"10.8.0.7"}]}`)
	end := scrubRow(scrubT0.Add(2*time.Minute), `{"clients":[{"name":"p-a","status":{"assignedIp":"10.8.0.7"}}]}`)
	otherInside := scrubRow(scrubT0.Add(90*time.Second), `{"clients":[{"name":"p-c","status":{"assignedIp":"10.8.0.7"}}]}`)
	after := scrubRow(scrubT0.Add(time.Hour), `{"clients":[{"name":"p-c","status":{"assignedIp":"10.8.0.7"}}],"policies":[{"allowedIps":["10.8.0.7/32"]}]}`)
	data := archiveOf(t, own, delta, end, otherInside, after)

	windows, err := eventAnalyticsModel.LabArchiveClientIPWindows(codec, data, []string{"p-a"})
	require.NoError(t, err)
	require.Equal(t, []eventAnalyticsModel.LabIPWindow{{
		TeamID: scrubTeam, Group: "grp", IP: "10.8.0.7", From: scrubT0, To: scrubT0.Add(2 * time.Minute),
	}}, windows)

	out, changed, err := eventAnalyticsModel.ScrubLabArchive(codec, data,
		map[string]eventAnalyticsModel.LabArchiveErasure{"p-a": {Pseudonym: "deleted-1", Windows: windows}})
	require.NoError(t, err)
	require.True(t, changed)
	rows := strings.Split(linesOf(t, out), "\n")
	require.Len(t, rows, 5)
	require.NotContains(t, rows[0], `"10.8.0.7"`)
	require.NotContains(t, rows[0], `"10.8.0.7/32"`)
	require.Contains(t, rows[0], `"10.8.0.9"`, "another client keeps its IP")
	require.Contains(t, rows[0], "gw 10.8.0.7", "text that merely mentions the address stays")
	require.Contains(t, rows[0], `"deleted-1"`)
	require.NotContains(t, rows[1], "10.8.0.7", "a delta that names no client, inside the window")
	require.NotContains(t, rows[2], `"10.8.0.7"`)
	require.Contains(t, rows[3], `"10.8.0.7"`, "inside the window but the address is explicitly another client's")
	require.Contains(t, rows[4], `"10.8.0.7"`, "after the window")
	require.Contains(t, rows[4], `"10.8.0.7/32"`)

	again, changed, err := eventAnalyticsModel.ScrubLabArchive(codec, out,
		map[string]eventAnalyticsModel.LabArchiveErasure{"p-a": {Pseudonym: "deleted-1", Windows: windows}})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, out, again)
}

// The collection fails closed: a row that names the person but cannot be read is an error, not a silent skip; one odd
// client entry does not hide the others, and a row that names nobody of interest is not looked at.
func TestLabArchiveClientIPWindows_FailsClosed(t *testing.T) {
	codec := eventAnalyticsModel.LabArchiveCodecZstdNDJSON

	odd := archiveOf(t, scrubRow(scrubT0, `{"clients":[{"name":"p-b","status":"odd"},{"name":"p-a","status":{"assignedIp":"10.8.0.7"}}]}`))
	windows, err := eventAnalyticsModel.LabArchiveClientIPWindows(codec, odd, []string{"p-a"})
	require.NoError(t, err)
	require.Len(t, windows, 1, "the readable client is collected next to an odd one")
	require.Equal(t, "10.8.0.7", windows[0].IP)

	for name, row := range map[string]string{
		"payload is not an object": `{"payload":"p-a"}`,
		"clients is not a list":    `{"payload":{"clients":"p-a"}}`,
		"the person's entry odd":   scrubRow(scrubT0, `{"clients":[{"name":"p-a","status":{"assignedIp":7}}]}`),
		"row is not json":          `p-a is here {`,
	} {
		_, err = eventAnalyticsModel.LabArchiveClientIPWindows(codec, archiveOf(t, row), []string{"p-a"})
		require.Error(t, err, name)
	}

	other := archiveOf(t, `{"payload":"somebody else"}`, `garbage`)
	_, err = eventAnalyticsModel.LabArchiveClientIPWindows(codec, other, []string{"p-a"})
	require.NoError(t, err)

	// A scrub that meets a row with a window's IP it cannot read does not guess.
	_, _, err = eventAnalyticsModel.ScrubLabArchive(codec, archiveOf(t, `"10.8.0.7" is here {`),
		map[string]eventAnalyticsModel.LabArchiveErasure{"p-a": {Pseudonym: "x", Windows: []eventAnalyticsModel.LabIPWindow{{IP: "10.8.0.7"}}}})
	require.Error(t, err)
}
