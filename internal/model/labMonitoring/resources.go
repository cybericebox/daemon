package labMonitoring

import (
	"encoding/json"
	"strconv"
)

// Resources is what a lab group uses, summed over the devices of all its labs.
// Live usage comes from the cluster's metrics (Available); the requested
// amounts are the fallback. Known is false when no device was observed.
type Resources struct {
	Known           bool
	Available       bool
	CPUMillicores   int64
	MemoryBytes     int64
	RequestedCPU    int64
	RequestedMemory int64
}

// Add folds another lab group's resources into the sum.
func (r *Resources) Add(other Resources) {
	if !other.Known {
		return
	}
	r.Known = true
	r.Available = r.Available || other.Available
	r.CPUMillicores += other.CPUMillicores
	r.MemoryBytes += other.MemoryBytes
	r.RequestedCPU += other.RequestedCPU
	r.RequestedMemory += other.RequestedMemory
}

// flexInt reads a protojson int64, which is encoded as a string.
type flexInt int64

func (f *flexInt) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return err
		}
		*f = flexInt(value)
		return nil
	}
	var number int64
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*f = flexInt(number)
	return nil
}

// PayloadResources sums the devices of every lab in one lab group's current
// monitoring state. A payload that cannot be read counts as nothing observed.
func PayloadResources(raw json.RawMessage) Resources {
	var payload struct {
		Labs []struct {
			Status struct {
				Devices []struct {
					UsageAvailable  bool    `json:"usageAvailable"`
					CPU             flexInt `json:"cpuMillicores"`
					Memory          flexInt `json:"memoryBytes"`
					RequestedCPU    flexInt `json:"cpuRequestMillicores"`
					RequestedMemory flexInt `json:"memoryRequestBytes"`
				} `json:"devices"`
			} `json:"status"`
		} `json:"labs"`
	}
	var out Resources
	if json.Unmarshal(raw, &payload) != nil {
		return out
	}
	for _, lab := range payload.Labs {
		for _, d := range lab.Status.Devices {
			out.Known = true
			out.RequestedCPU += int64(d.RequestedCPU)
			out.RequestedMemory += int64(d.RequestedMemory)
			// A running container always uses some memory: zero CPU and memory together means
			// the metrics are not in yet, not a measurement.
			if d.UsageAvailable && (d.CPU > 0 || d.Memory > 0) {
				out.Available = true
				out.CPUMillicores += int64(d.CPU)
				out.MemoryBytes += int64(d.Memory)
			}
		}
	}
	return out
}
