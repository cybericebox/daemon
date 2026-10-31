package eventStandModel

import (
	"encoding/json"
	"fmt"
)

// WaitDetail is what a waiting Lab needs and what the event reservation has left, so the organizers read "needs X,
// has Y". The storage fields are zero unless the snapshot quota is the short room.
type WaitDetail struct {
	NeedCPUMillicores int64 `json:"need_cpu_millicores"`
	NeedMemoryBytes   int64 `json:"need_memory_bytes"`
	HaveCPUMillicores int64 `json:"have_cpu_millicores"`
	HaveMemoryBytes   int64 `json:"have_memory_bytes"`
	NeedStorageBytes  int64 `json:"need_storage_bytes"`
	HaveStorageBytes  int64 `json:"have_storage_bytes"`
}

// WaitDetailText is the English fallback of the detail (" (needs ..., has ...)"), empty when there is none or it
// does not read.
func WaitDetailText(raw string) string {
	if raw == "" {
		return ""
	}
	var d WaitDetail
	if json.Unmarshal([]byte(raw), &d) != nil {
		return ""
	}
	if d.NeedStorageBytes > 0 || d.HaveStorageBytes > 0 {
		return fmt.Sprintf(" (needs %d MiB of snapshots, has %d MiB)", d.NeedStorageBytes>>20, d.HaveStorageBytes>>20)
	}
	return fmt.Sprintf(" (needs %dm CPU and %d MiB, has %dm CPU and %d MiB)", d.NeedCPUMillicores, d.NeedMemoryBytes>>20, d.HaveCPUMillicores, d.HaveMemoryBytes>>20)
}
