package resourceCalendarUseCase

import (
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"strconv"
	"time"
)

type ObservationCompute struct {
	CPUMillicores string `json:"CPUMillicores"`
	MemoryBytes   string `json:"MemoryBytes"`
}
type ObservationHeld struct {
	CPUMillicores      string `json:"CPUMillicores"`
	MemoryBytes        string `json:"MemoryBytes"`
	SnapshotQuotaBytes string `json:"SnapshotQuotaBytes"`
}
type ResourceObservation struct {
	ObservedAt                    *time.Time         `json:"ObservedAt" extensions:"x-nullable"`
	Complete                      bool               `json:"Complete"`
	Held                          ObservationHeld    `json:"Held"`
	PendingStarts                 ObservationCompute `json:"PendingStarts"`
	GroupServices                 ObservationCompute `json:"GroupServices"`
	PhysicalStorageBytesAvailable bool               `json:"PhysicalStorageBytesAvailable"`
	PhysicalStorageBytes          string             `json:"PhysicalStorageBytes"`
}

func ObservationView(t eventLabModel.ResourceTotals) ResourceObservation {
	compute := func(c eventLabModel.Compute) ObservationCompute {
		return ObservationCompute{strconv.FormatInt(c.CPUMillicores, 10), strconv.FormatInt(c.MemoryBytes, 10)}
	}
	return ResourceObservation{ObservedAt: t.ObservedAt, Complete: t.Complete, Held: ObservationHeld{strconv.FormatInt(t.Held.CPUMillicores, 10), strconv.FormatInt(t.Held.MemoryBytes, 10), strconv.FormatInt(t.Storage.SnapshotQuotaBytes, 10)}, PendingStarts: compute(t.PendingStarts), GroupServices: compute(t.GroupServices), PhysicalStorageBytesAvailable: t.Storage.PhysicalKnown, PhysicalStorageBytes: strconv.FormatInt(t.Storage.PhysicalStorageBytes, 10)}
}
