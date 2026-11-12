package lab

import (
	"context"
	"time"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labCheckModel "github.com/cybericebox/daemon/internal/model/labCheck"
)

// ChecksStore keeps the results of the laboratory checks.
type ChecksStore interface {
	Record(ctx context.Context, res labCheckModel.Result, now time.Time) error
	NoteDetail(ctx context.Context, group, lab, checkID, detail string, now time.Time) error
}

// ChecksSink hears the labs of every monitoring message (snapshot or delta): the results of their checks are in
// Status.Checks. It must not block for long; a failure is logged by the runner and never ends the stream.
type ChecksSink func(ctx context.Context, labs []*labpb.Lab, observedAt time.Time) error

// NewChecksRecorder records the check results of the labs a message carries.
//
//   - passed / not passed: stored (first_passed_at never goes back, changed_at moves when the state changes);
//   - error ("could not be made"): the stored state stays as it was, only the detail for staff is refreshed;
//   - unknown (no result yet): ignored;
//   - an id this platform did not make (it does not map back to a task key) is ignored.
//
// The times are the laboratory's own (first_passed_unix_ms, changed_unix_ms); the time of the message stands in when it gives none.
func NewChecksRecorder(store ChecksStore, now func() time.Time) ChecksSink {
	return func(ctx context.Context, labs []*labpb.Lab, observedAt time.Time) error {
		stamp := now()
		for _, l := range labs {
			for _, result := range l.GetStatus().GetChecks() {
				if _, ours := exerciseModel.TaskKeyOfCheck(result.GetId()); !ours {
					continue
				}
				switch result.GetState() {
				case labpb.CheckState_CHECK_STATE_PASSED, labpb.CheckState_CHECK_STATE_NOT_PASSED:
					res := labCheckModel.Result{
						Group: l.GetLabGroupName(), Lab: l.GetName(), CheckID: result.GetId(),
						State: labCheckModel.StateNotPassed, ChangedAt: millis(result.GetChangedUnixMs(), observedAt),
						Value: result.GetValue(), Detail: result.GetDetail(), ObservedAt: observedAt,
					}
					if result.GetState() == labpb.CheckState_CHECK_STATE_PASSED {
						res.State = labCheckModel.StatePassed
					}
					if first := result.GetFirstPassedUnixMs(); first > 0 {
						at := time.UnixMilli(first).UTC()
						res.FirstPassedAt = &at
					} else if res.State == labCheckModel.StatePassed {
						// a pass without the laboratory's own first-pass time: the change is the first pass seen
						at := res.ChangedAt
						res.FirstPassedAt = &at
					}
					if err := store.Record(ctx, res, stamp); err != nil {
						return err
					}
				case labpb.CheckState_CHECK_STATE_ERROR:
					if err := store.NoteDetail(ctx, l.GetLabGroupName(), l.GetName(), result.GetId(), result.GetDetail(), stamp); err != nil {
						return err
					}
				case labpb.CheckState_CHECK_STATE_UNKNOWN:
				}
			}
		}
		return nil
	}
}

func millis(ms int64, fallback time.Time) time.Time {
	if ms <= 0 {
		return fallback
	}
	return time.UnixMilli(ms).UTC()
}
