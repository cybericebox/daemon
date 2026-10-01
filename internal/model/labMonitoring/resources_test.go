package labMonitoring

import (
	"encoding/json"
	"testing"
)

func TestPayloadResourcesTreatsZeroUsageAsNotMeasuredYet(t *testing.T) {
	got := PayloadResources(json.RawMessage(`{"labs":[{"status":{"devices":[
		{"usageAvailable":true,"cpuMillicores":"0","memoryBytes":"0","cpuRequestMillicores":"100","memoryRequestBytes":"64"},
		{"usageAvailable":true,"cpuMillicores":"0","memoryBytes":"16777216"}]}}]}`))
	if !got.Known || !got.Available || got.CPUMillicores != 0 || got.MemoryBytes != 16777216 || got.RequestedCPU != 100 {
		t.Fatalf("got %+v", got)
	}
	none := PayloadResources(json.RawMessage(`{"labs":[{"status":{"devices":[{"usageAvailable":true,"cpuMillicores":0,"memoryBytes":0}]}}]}`))
	if !none.Known || none.Available {
		t.Fatalf("zero usage must not count as measured: %+v", none)
	}
}

func TestPayloadLaunchReadsQueueAndWarnings(t *testing.T) {
	got := PayloadLaunch(json.RawMessage(`{"groups":[{"status":{"imageWarning":"vpn:latest"}}],"labs":[
		{"status":{"phase":"Queued","scheduling":{"position":5,"length":"9","reason":"InFlightLimit"}}},
		{"status":{"phase":"Queued","scheduling":{"position":2,"length":9,"reason":"PreparingImages"}}},
		{"status":{"phase":"Ready"}}]}`))
	if got.QueuedLabs != 2 || got.Position != 2 || got.Length != 9 || got.Reason != "PreparingImages" || !got.ImageWarning {
		t.Fatalf("PayloadLaunch = %+v", got)
	}
	if none := PayloadLaunch(json.RawMessage(`broken`)); none.QueuedLabs != 0 || none.ImageWarning {
		t.Fatalf("unreadable payload = %+v", none)
	}
}
