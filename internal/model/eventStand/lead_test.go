package eventStandModel

import (
	"testing"
	"time"
)

func TestComputeLeadFormula(t *testing.T) {
	cases := []struct {
		name string
		in   LeadInput
		want time.Duration
	}{
		{"no workload is the floor", LeadInput{}, MinLead},
		{"small workload is the floor", LeadInput{Workload: 10, MaxPods: 100}, MinLead},
		// 5 waves x 90 s x 1.5 = 11 m 15 s
		{"waves x avg x 1.5", LeadInput{Workload: 500, MaxPods: 100}, 11*time.Minute + 15*time.Second},
		// 20 waves x 90 s x 1.5 = 45 m, plus 5 m of pre-pull
		{"pre-pull is added", LeadInput{Workload: 2000, MaxPods: 100, PrePull: 5 * time.Minute}, 50 * time.Minute},
		{"avg pod start", LeadInput{Workload: 1000, MaxPods: 100, AvgPodStart: 2 * time.Minute}, 30 * time.Minute},
		{"no pods limit is one wave", LeadInput{Workload: 100000}, MinLead},
		{"capped", LeadInput{Workload: 1000000, MaxPods: 1}, MaxLead},
	}
	for _, c := range cases {
		if got := ComputeLead(c.in); got != c.want {
			t.Errorf("%s: lead = %v, want %v", c.name, got, c.want)
		}
	}
	if (LeadInput{Workload: 250, MaxPods: 100}).Waves() != 3 {
		t.Fatal("waves must round up")
	}
	if (LeadInput{Workload: 0, MaxPods: 100}).Waves() != 0 {
		t.Fatal("no workload, no waves")
	}
}

func TestDeployLeadIsNoLongerASetting(t *testing.T) {
	// the organizer's stand settings keep only the teardown delay
	if got := DefaultTiming(); got.TeardownDelayMinutes != DefaultTeardownDelayMinutes {
		t.Fatalf("default timing = %+v", got)
	}
}
