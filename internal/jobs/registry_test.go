package jobsRegistry

import "testing"

func TestPeriodicJobsExcludeLaboratoryWorkWithoutConfiguredAgent(t *testing.T) {
	withoutLabs := NewWorkerRegistry(nil, false).PeriodicJobs()
	withLabs := NewWorkerRegistry(nil, true).PeriodicJobs()
	if len(withLabs) != len(withoutLabs)+3 {
		t.Fatalf("laboratory schedules: got %d with agent vs %d without, want exactly three omitted jobs", len(withLabs), len(withoutLabs))
	}
}
