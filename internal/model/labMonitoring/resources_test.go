package labMonitoring

import (
	"encoding/json"
	"testing"
	"time"
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

// A withdrawn lab is queued: the stand shows its attempt and retry time (the most of its pods).
func TestPayloadLaunchReadsAWithdrawal(t *testing.T) {
	got := PayloadLaunch(json.RawMessage(`{"labs":[
		{"status":{"phase":"Queued","scheduling":{"position":1,"length":3,"reason":"Withdrawn"},"devices":[
			{"scheduling":{"state":"POD_STATE_QUEUED","withdrawals":2,"retryAfterUnixMs":"1792400000000"}},
			{"scheduling":{"state":"POD_STATE_QUEUED","withdrawals":3,"retryAfterUnixMs":"1792400060000"}}]}}]}`))
	if got.QueuedLabs != 1 || got.Reason != "Withdrawn" || got.Attempt != 3 || !got.RetryAt.Equal(time.UnixMilli(1792400060000)) {
		t.Fatalf("PayloadLaunch = %+v", got)
	}
}

// A lab whose sent pods wait for a node is not queued; the stand counts it apart, since its earliest starting pod was sent.
func TestPayloadLaunchCountsLabsWaitingForANode(t *testing.T) {
	got := PayloadLaunch(json.RawMessage(`{"labs":[
		{"status":{"phase":"Provisioning","scheduling":{"reason":"NodeStarting","pods":1},"devices":[
			{"scheduling":{"state":"POD_STATE_STARTING","dispatchedUnixMs":"1792400020000"}}]}},
		{"status":{"phase":"Provisioning","scheduling":{"reason":"NodeStarting"},"devices":[
			{"scheduling":{"state":"POD_STATE_STARTING","dispatchedUnixMs":"1792400030000"}}]}}]}`))
	if got.QueuedLabs != 0 || got.NodeStartingLabs != 2 || !got.WaitingSince.Equal(time.UnixMilli(1792400020000)) {
		t.Fatalf("PayloadLaunch = %+v", got)
	}
	var sum Launch
	sum.Add(got)
	sum.Add(PayloadLaunch(json.RawMessage(`{"labs":[{"status":{"phase":"Queued","scheduling":{"position":4,"length":9,"reason":"InFlightLimit"}}}]}`)))
	if sum.QueuedLabs != 1 || sum.Reason != "InFlightLimit" || sum.NodeStartingLabs != 2 || !sum.WaitingSince.Equal(time.UnixMilli(1792400020000)) {
		t.Fatalf("sum = %+v", sum)
	}
}
