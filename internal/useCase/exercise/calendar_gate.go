package exercise

import (
	"context"
	"crypto/sha256"
	"fmt"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	calendarUseCase "github.com/cybericebox/daemon/internal/useCase/resourceCalendar"
)

// TestLabGate is the resource calendar's admission of a catalog author's test laboratory: inside the author's
// booked window, the guaranteed pool, or room no event has reserved. It fails with "no free resources now" and
// the nearest free window the author can book. The hold is released when the laboratory ends.
type TestLabGate interface {
	AdmitTestLab(ctx context.Context, req calendarUseCase.TestLabRequest) (calendarUseCase.TestLabRoom, error)
	ReleaseTestLab(ctx context.Context, id uuid.UUID) error
	ExtendTestLab(ctx context.Context, id, owner uuid.UUID, size calendarUseCase.Amount, until time.Time) error
}

// SetTestLabGate wires the calendar after the use cases exist; nil admits every test laboratory.
func (u *ExerciseUseCase) SetTestLabGate(g TestLabGate) { u.testLabGate = g }

// admitTestLab asks the calendar for room for the laboratory of this topology; the hold is keyed by the deploy id.
func (u *ExerciseUseCase) admitTestLab(ctx context.Context, id, owner uuid.UUID, topo exerciseModel.Topology) error {
	if u.testLabGate == nil {
		return nil
	}
	policy := u.Policy()
	var largest calendarUseCase.Amount
	for _, d := range policy.Devices(topo) {
		largest = largest.Max(d.Amount)
	}
	_, err := u.testLabGate.AdmitTestLab(ctx, calendarUseCase.TestLabRequest{
		ID: id, Owner: owner, Size: policy.Total(topo).Amount, LargestDevice: largest, Lease: u.testDeployTTL(),
	})
	return err
}

// admitStaticTest asks the calendar to admit a static test, which reserves nothing.
func (u *ExerciseUseCase) admitStaticTest(ctx context.Context, id, owner uuid.UUID) error {
	if u.testLabGate == nil {
		return nil
	}
	_, err := u.testLabGate.AdmitTestLab(ctx, calendarUseCase.TestLabRequest{ID: id, Owner: owner, Static: true, Lease: u.testDeployTTL()})
	return err
}

// releaseTestLab frees the calendar room of a test laboratory that ended; a failure only leaves the hold to
// expire with its lease.
func (u *ExerciseUseCase) releaseTestLab(ctx context.Context, id uuid.UUID) {
	if u.testLabGate != nil {
		_ = u.testLabGate.ReleaseTestLab(ctx, id)
	}
}

type eventTestLabGate interface {
	AdmitEventTestLab(context.Context, calendarUseCase.EventTestLabRequest) (calendarUseCase.TestLabRoom, error)
	CheckEventTestLabRoom(context.Context, calendarUseCase.EventTestLabRequest) (calendarUseCase.TestLabRoom, error)
	EventPreviewAllocated(context.Context, uuid.UUID) (bool, error)
	ReleaseEventTestLab(context.Context, uuid.UUID, uuid.UUID) error
	EventPreviewCleanupCandidates(context.Context) ([]calModel.TestLabHold, error)
	ExtendEventTestLab(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) (time.Time, error)
}

func eventPreviewTestGroup(eventID, owner uuid.UUID) string {
	hash := sha256.Sum256(append(eventID.Bytes(), owner.Bytes()...))
	return fmt.Sprintf("tp-%x", hash[:16])
}
func (u *ExerciseUseCase) eventPreviewLabRequest(eventID, id, owner uuid.UUID, group string, topo exerciseModel.Topology) calendarUseCase.EventTestLabRequest {
	policy := u.Policy()
	var largest calendarUseCase.Amount
	for _, d := range policy.Devices(topo) {
		largest = largest.Max(d.Amount)
	}
	return calendarUseCase.EventTestLabRequest{EventID: eventID, Group: group, TestLabRequest: calendarUseCase.TestLabRequest{ID: id, Owner: owner, Size: policy.Total(topo).Amount, LargestDevice: largest, Lease: u.testDeployTTL()}}
}
func (u *ExerciseUseCase) EventPreviewAllocated(ctx context.Context, eventID uuid.UUID) (bool, error) {
	gate, ok := u.testLabGate.(eventTestLabGate)
	if !ok {
		return false, nil
	}
	return gate.EventPreviewAllocated(ctx, eventID)
}
func (u *ExerciseUseCase) CanStartEventPreview(ctx context.Context, eventID, versionID, variantID uuid.UUID) (bool, error) {
	variant, err := u.loadVariant(ctx, versionID, variantID)
	if err != nil {
		return false, err
	}
	if len(variant.Topology.Devices) == 0 {
		return true, nil
	}
	gate, ok := u.testLabGate.(eventTestLabGate)
	if !ok {
		return false, nil
	}
	overhead, known := u.eventPreviewGroupOverhead()
	if !known {
		return false, nil
	}
	req := u.eventPreviewLabRequest(eventID, uuid.Nil, uuid.Nil, "", variant.Topology)
	req.GroupOverhead = overhead
	room, err := gate.CheckEventTestLabRoom(ctx, req)
	if err != nil {
		return false, nil
	}
	return room.Available, nil
}
func (u *ExerciseUseCase) releaseScopedTestLab(ctx context.Context, deploy exerciseModel.TestDeploy) error {
	if deploy.EventID == nil {
		u.releaseTestLab(ctx, deploy.ID)
		return nil
	}
	if gate, ok := u.testLabGate.(eventTestLabGate); ok {
		return gate.ReleaseEventTestLab(ctx, *deploy.EventID, deploy.ID)
	}
	return calendarError()
}
func (u *ExerciseUseCase) requireScopedPreviewActive(ctx context.Context, deploy exerciseModel.TestDeploy) error {
	if deploy.EventID == nil {
		return nil
	}
	allowed, err := u.EventPreviewAllocated(ctx, *deploy.EventID)
	if err != nil {
		return err
	}
	if !allowed {
		return calModel.ErrNotEnoughReserved.Err()
	}
	return nil
}

func calendarError() error { return calModel.ErrNotEnoughReserved.Err() }

func (u *ExerciseUseCase) eventPreviewGroupPlan() infraModel.GroupPlan {
	return infraModel.GroupPlan{MaxUsers: 1, MaxActiveLabs: u.MaxActiveTestDeploys(), InternetLabs: u.MaxActiveTestDeploys(), AllowedRelations: u.MaxActiveTestDeploys()}
}
func (u *ExerciseUseCase) eventPreviewGroupOverhead() (calendarUseCase.Amount, bool) {
	sizer, ok := u.infra.(interface {
		GroupSizes(infraModel.GroupPlan) (infraModel.GroupSizes, bool)
	})
	if !ok {
		return calendarUseCase.Amount{}, false
	}
	sizes, known := sizer.GroupSizes(u.eventPreviewGroupPlan())
	return sizes.Total(), known && sizes.VPN.CPUMillicores > 0 && sizes.VPN.MemoryBytes > 0 && sizes.Gateway.CPUMillicores > 0 && sizes.Gateway.MemoryBytes > 0
}
