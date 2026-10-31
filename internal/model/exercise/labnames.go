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

// ReservedDeviceNames are the names of the endpoints the platform provides in every lab (the VPN, the internet
// gateway); the laboratory refuses a device with one of them. Keep in sync with laboratory
// pkg/names.ReservedDeviceNames (TestReservedDeviceNamesMatchTheLaboratory checks it when that source is at hand;
// once the pinned laboratory release has the package, import it instead).
var ReservedDeviceNames = []string{string(EndpointVPN), "gateway", string(EndpointInternet)}

// ValidateDeviceNames refuses the first device of the variants named like a platform endpoint. A switch or a hub has
// a display name only and is named in the lab by its id, so it is not checked.
func ValidateDeviceNames(variants []Variant) error {
	for vi, variant := range variants {
		for _, device := range variant.Topology.Devices {
			if !device.IsNode() {
				continue
			}
			for _, reserved := range ReservedDeviceNames {
				if strings.EqualFold(strings.TrimSpace(device.Name), reserved) {
					return ErrDeviceNameReserved.WithPublicContext("variant", vi+1).
						WithPublicContext("device", device.Name).
						Err()
				}
			}
		}
	}
	return nil
}
