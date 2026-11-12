package lab

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	labCheckModel "github.com/cybericebox/daemon/internal/model/labCheck"
)

type memoryChecks struct {
	recorded []labCheckModel.Result
	notes    []string
}

func (m *memoryChecks) Record(_ context.Context, r labCheckModel.Result, _ time.Time) error {
	m.recorded = append(m.recorded, r)
	return nil
}

func (m *memoryChecks) NoteDetail(_ context.Context, group, lab, id, detail string, _ time.Time) error {
	m.notes = append(m.notes, group+"/"+lab+"/"+id+": "+detail)
	return nil
}

func labWithChecks(group, name string, checks ...*labpb.CheckResult) *labpb.Lab {
	return &labpb.Lab{LabGroupName: group, Name: name, Status: &labpb.LabStatus{Checks: checks}}
}

func TestChecksRecorderMapsLaboratoryResultsToStoredOnes(t *testing.T) {
	store := &memoryChecks{}
	observed := time.UnixMilli(1_700_000_100_000).UTC()
	record := NewChecksRecorder(store, func() time.Time { return observed })

	err := record(context.Background(), []*labpb.Lab{
		labWithChecks("g", "l",
			&labpb.CheckResult{Id: "ospf:1", State: labpb.CheckState_CHECK_STATE_PASSED, FirstPassedUnixMs: 1_700_000_050_000, ChangedUnixMs: 1_700_000_060_000, Value: "7", Detail: "all full"},
			&labpb.CheckResult{Id: "ospf:2", State: labpb.CheckState_CHECK_STATE_NOT_PASSED, ChangedUnixMs: 1_700_000_070_000, Detail: "neighbour down"},
			&labpb.CheckResult{Id: "ospf:3", State: labpb.CheckState_CHECK_STATE_ERROR, Detail: "device not ready"},
			&labpb.CheckResult{Id: "ospf:4", State: labpb.CheckState_CHECK_STATE_UNKNOWN},
			&labpb.CheckResult{Id: "someone-elses", State: labpb.CheckState_CHECK_STATE_PASSED},
			// a pass the laboratory gave no first-pass time for: the change is the first pass seen
			&labpb.CheckResult{Id: "quick:1", State: labpb.CheckState_CHECK_STATE_PASSED, ChangedUnixMs: 1_700_000_080_000},
		),
		{LabGroupName: "g", Name: "no-status"},
	}, observed)
	require.NoError(t, err)

	require.Len(t, store.recorded, 3)
	first := store.recorded[0]
	require.Equal(t, "g", first.Group)
	require.Equal(t, "l", first.Lab)
	require.Equal(t, "ospf:1", first.CheckID)
	require.Equal(t, labCheckModel.StatePassed, first.State)
	require.Equal(t, time.UnixMilli(1_700_000_050_000).UTC(), first.FirstPassedAt.UTC())
	require.Equal(t, time.UnixMilli(1_700_000_060_000).UTC(), first.ChangedAt)
	require.Equal(t, "7", first.Value)
	require.Equal(t, "all full", first.Detail, "the detail is kept for staff")

	require.Equal(t, labCheckModel.StateNotPassed, store.recorded[1].State)
	require.Nil(t, store.recorded[1].FirstPassedAt)

	require.Equal(t, time.UnixMilli(1_700_000_080_000).UTC(), store.recorded[2].FirstPassedAt.UTC())

	// An error keeps the stored state: only the detail is refreshed.
	require.Equal(t, []string{"g/l/ospf:3: device not ready"}, store.notes)
}
