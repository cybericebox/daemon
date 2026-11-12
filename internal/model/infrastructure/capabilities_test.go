package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func full() *AgentFeatures {
	return &AgentFeatures{
		Checks:    &ChecksFeature{Enabled: true, Observations: []string{"file", "custom", "directory"}},
		Access:    &AccessFeature{HTTP: true, Terminal: true},
		Bootstrap: &BootstrapFeature{},
		VPN:       &VPNFeature{ReverseConnections: true},
	}
}

func old() *AgentFeatures {
	return &AgentFeatures{Checks: &ChecksFeature{Enabled: true}, Bootstrap: &BootstrapFeature{}}
}

// Per option: all when every reporting laboratory can, none when none can, some otherwise. A laboratory that has not
// reported turns "all" into "some" and "none" into "unknown"; with no report at all everything is "unknown".
func TestCapabilitiesOfAggregates(t *testing.T) {
	none := CapabilitiesOf(nil)
	require.False(t, none.Known)
	require.Equal(t, AvailabilityUnknown, none.Checks)
	require.Equal(t, AvailabilityUnknown, none.Observations["file"])
	require.Len(t, none.Observations, 10)
	require.Len(t, none.Access, 4)
	require.Equal(t, none, CapabilitiesOf([]*AgentFeatures{nil, nil}), "agents that have not reported are unknown, not false")

	same := CapabilitiesOf([]*AgentFeatures{full(), full()})
	require.True(t, same.Known)
	require.Equal(t, AvailabilityAll, same.Observations["directory"])
	require.Equal(t, AvailabilityAll, same.Observations["file.checksum"])
	require.Equal(t, AvailabilityNone, same.Observations["permission"], "no laboratory runs it yet")
	require.Equal(t, map[string]Availability{"http": AvailabilityAll, "terminal": AvailabilityAll, "vnc": AvailabilityNone, "rdp": AvailabilityNone}, same.Access)
	require.Equal(t, AvailabilityAll, same.Bootstrap)
	require.Equal(t, AvailabilityAll, same.VPNReverse)

	mixed := CapabilitiesOf([]*AgentFeatures{full(), old()})
	require.Equal(t, AvailabilityAll, mixed.Observations["file"], "both run file")
	require.Equal(t, AvailabilitySome, mixed.Observations["directory"], "only one runs directory")
	require.Equal(t, AvailabilitySome, mixed.Access["terminal"])
	require.Equal(t, AvailabilitySome, mixed.VPNReverse)
	require.Equal(t, AvailabilityAll, mixed.Checks)

	pending := CapabilitiesOf([]*AgentFeatures{full(), nil})
	require.True(t, pending.Known)
	require.Equal(t, AvailabilitySome, pending.Observations["file"], "one that has not reported may not run it")
	require.Equal(t, AvailabilityUnknown, pending.Observations["permission"], "none reporting yes, one still unknown: do not forbid")
	require.Equal(t, AvailabilityNone, CapabilitiesOf([]*AgentFeatures{old(), old()}).Access["terminal"])
}

// The access kinds come from what the laboratory reports; one that reports none serves http only; vnc and rdp are never served.
func TestServesAccess(t *testing.T) {
	require.True(t, (AgentFeatures{}).ServesAccess("http"))
	require.False(t, (AgentFeatures{}).ServesAccess("terminal"), "an agent older than the field serves http only")
	reported := AgentFeatures{Access: &AccessFeature{HTTP: true, Terminal: true}}
	require.True(t, reported.ServesAccess("terminal"))
	require.False(t, reported.ServesAccess("vnc"))
	require.False(t, reported.ServesAccess("rdp"))
	require.False(t, reported.ServesAccess("ssh"))
	require.True(t, (&ChecksFeature{Observations: []string{"file"}}).Offers("file.checksum"), "the file keys come with a listed file")
	require.False(t, (&ChecksFeature{Observations: []string{"custom"}}).Offers("file.type"))
}

// The console address is the reported console host (the default when none) and the reported base domain; nothing while the
// console is not served or the domain is unknown.
func TestConsoleAddress(t *testing.T) {
	served := AgentFeatures{Access: &AccessFeature{HTTP: true, Terminal: true}, Endpoints: EndpointsFeature{LabsDomain: "labs.example.com"}}
	host, domain, ok := served.ConsoleAddress()
	require.True(t, ok)
	require.Equal(t, "terminal", host, "the default when the agent reports none")
	require.Equal(t, "labs.example.com", domain)

	served.Access.TerminalHost = "shell"
	host, _, _ = served.ConsoleAddress()
	require.Equal(t, "shell", host)

	notServed := served
	notServed.Access = &AccessFeature{HTTP: true, TerminalHost: "shell"}
	_, _, ok = notServed.ConsoleAddress()
	require.False(t, ok)
	_, _, ok = (AgentFeatures{Access: &AccessFeature{Terminal: true}}).ConsoleAddress()
	require.False(t, ok, "no base domain reported")
}
