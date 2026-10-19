package jobsRegistry

import "testing"

func TestPeriodicJobsExcludeLaboratoryWorkWithoutConfiguredAgent(t *testing.T) {
	withoutLabs := NewWorkerRegistry(nil, false, 0).PeriodicJobs()
	withLabs := NewWorkerRegistry(nil, true, 0).PeriodicJobs()
	if len(withLabs) != len(withoutLabs)+6 {
		t.Fatalf("laboratory schedules: got %d with agent vs %d without, want exactly six omitted jobs", len(withLabs), len(withoutLabs))
	}
}
