package exerciseModel

import (
	"strconv"
	"strings"
)

// LabDeviceNames returns the name every device of the topology gets inside the lab, index-aligned
// with devices. A container keeps its logical name; a forwarding device (switch, hub) is named
// "sw-" + the 32 hex digits of its id (35 chars: fits the lab device name limit), with the tail
// replaced by a counter when that name is taken.
func LabDeviceNames(devices []Device) []string {
	used := make(map[string]bool, len(devices))
	for _, d := range devices {
		if !d.Type.IsForwarding() {
			used[d.Name] = true
		}
	}
	names := make([]string, len(devices))
	for i, d := range devices {
		name := d.Name
		if d.Type.IsForwarding() {
			base := "sw-" + strings.ReplaceAll(d.ID.String(), "-", "")
			name = base
			for n := 0; used[name]; n++ {
				suffix := strconv.Itoa(n)
				name = base[:len(base)-len(suffix)] + suffix
			}
			used[name] = true
		}
		names[i] = name
	}
	return names
}
