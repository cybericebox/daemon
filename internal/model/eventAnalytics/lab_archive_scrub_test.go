package eventAnalyticsModel_test

import (
	"strings"
	"testing"

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

// The IPs of a client found in an archive alone are collected, and scrubbing empties every JSON string equal to
// them (with or without /32) wherever it is, while other addresses and text that merely mentions one stay.
func TestScrubLabArchive_BlanksCollectedIPsEverywhere(t *testing.T) {
	row := `{"payload":{"clients":[{"name":"p-a","status":{"assignedIp":"10.8.0.7"}},{"name":"p-b","status":{"assignedIp":"10.8.0.9"}}],` +
		`"policies":[{"allowedIps":["10.8.0.7/32","10.8.0.9"],"note":"gw 10.8.0.7"}],"groups":[{"vpn":"10.8.0.7"}]}}`
	data := archiveOf(t, row)

	ips, err := eventAnalyticsModel.LabArchiveClientIPs(eventAnalyticsModel.LabArchiveCodecZstdNDJSON, data, []string{"p-a"})
	require.NoError(t, err)
	require.Equal(t, []string{"10.8.0.7"}, ips)

	out, changed, err := eventAnalyticsModel.ScrubLabArchive(eventAnalyticsModel.LabArchiveCodecZstdNDJSON, data,
		map[string]eventAnalyticsModel.LabArchiveErasure{"p-a": {Pseudonym: "deleted-1", IPs: ips}})
	require.NoError(t, err)
	require.True(t, changed)
	text := linesOf(t, out)
	require.NotContains(t, text, `"10.8.0.7"`)
	require.NotContains(t, text, `"10.8.0.7/32"`)
	require.NotContains(t, text, `"p-a"`)
	require.Contains(t, text, `"deleted-1"`)
	require.Contains(t, text, `"10.8.0.9"`, "another client keeps its IP")
	require.Contains(t, text, "gw 10.8.0.7", "text that merely mentions the address stays")

	again, changed, err := eventAnalyticsModel.ScrubLabArchive(eventAnalyticsModel.LabArchiveCodecZstdNDJSON, out,
		map[string]eventAnalyticsModel.LabArchiveErasure{"p-a": {Pseudonym: "deleted-1", IPs: ips}})
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, out, again)
}
