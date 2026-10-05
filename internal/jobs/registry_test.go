package jobsRegistry

import "testing"

func TestPeriodicJobsExcludeLaboratoryWorkWithoutConfiguredAgent(t *testing.T) {
	withoutLabs := NewWorkerRegistry(nil, false, 0).PeriodicJobs()
	withLabs := NewWorkerRegistry(nil, true, 0).PeriodicJobs()
	if len(withLabs) != len(withoutLabs)+4 {
		t.Fatalf("laboratory schedules: got %d with agent vs %d without, want exactly four omitted jobs", len(withLabs), len(withoutLabs))
	}
}
