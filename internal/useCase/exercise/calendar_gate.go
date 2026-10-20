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
func (u *ExerciseUseCase) admitTestLab(ctx context.Context, id, owner uuid.UUID, topo exerciseModel.Topology, lease time.Duration) error {
	if u.testLabGate == nil {
		return nil
	}
	policy := u.Policy()
	var largest calendarUseCase.Amount
	for _, d := range policy.Devices(topo) {
		largest = largest.Max(d.Amount)
	}
	_, err := u.testLabGate.AdmitTestLab(ctx, calendarUseCase.TestLabRequest{
		ID: id, Owner: owner, Size: policy.Total(topo).Amount, LargestDevice: largest, Lease: lease,
		PinnedAgent: u.testGroupAgent(ctx, owner),
	})
	return err
}

// testGroupAgent is the agent of the author's shared test group when it exists: a new run goes there.
func (u *ExerciseUseCase) testGroupAgent(ctx context.Context, owner uuid.UUID) *uuid.UUID {
	locator, ok := u.infra.(interface {
		GroupAgent(ctx context.Context, group string) (uuid.UUID, bool, error)
	})
	if !ok {
		return nil
	}
	agent, found, err := locator.GroupAgent(ctx, testGroupName(owner))
	if err != nil || !found {
		return nil
	}
	return &agent
}

// admitStaticTest asks the calendar to admit a static test, which reserves nothing.
func (u *ExerciseUseCase) admitStaticTest(ctx context.Context, id, owner uuid.UUID, lease time.Duration) error {
	if u.testLabGate == nil {
		return nil
	}
	_, err := u.testLabGate.AdmitTestLab(ctx, calendarUseCase.TestLabRequest{ID: id, Owner: owner, Static: true, Lease: lease})
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
	EventPreviewAllocated(context.Context, uuid.UUID) (bool, error)
	ReleaseEventTestLab(context.Context, uuid.UUID, uuid.UUID) error
	EventPreviewCleanupCandidates(context.Context) ([]calModel.TestLabHold, error)
	ExtendEventTestLab(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Time) (time.Time, error)
}

// legacyEventPreview is a preview lab started before previews ran on the author's lease: it lives in its own
// per-event group inside the event reservation, and keeps that path until it ends.
func legacyEventPreview(deploy exerciseModel.TestDeploy) bool {
	return deploy.EventID != nil && deploy.GroupName == eventPreviewTestGroup(*deploy.EventID, deploy.CreatedBy)
}

func eventPreviewTestGroup(eventID, owner uuid.UUID) string {
	hash := sha256.Sum256(append(eventID.Bytes(), owner.Bytes()...))
	return fmt.Sprintf("tp-%x", hash[:16])
}
func (u *ExerciseUseCase) EventPreviewAllocated(ctx context.Context, eventID uuid.UUID) (bool, error) {
	gate, ok := u.testLabGate.(eventTestLabGate)
	if !ok {
		return false, nil
	}
	return gate.EventPreviewAllocated(ctx, eventID)
}

// CanStartEventPreview says whether a preview of this variant may be started at all. Capacity is not asked here: a
// preview lab takes the author's lease at launch (like a studio test lab) and a full platform answers then.
func (u *ExerciseUseCase) CanStartEventPreview(ctx context.Context, eventID, versionID, variantID uuid.UUID) (bool, error) {
	variant, err := u.loadVariant(ctx, versionID, variantID)
	if err != nil {
		return false, err
	}
	return len(variant.Topology.Devices) == 0 || u.infra != nil, nil
}
func (u *ExerciseUseCase) releaseScopedTestLab(ctx context.Context, deploy exerciseModel.TestDeploy) error {
	if !legacyEventPreview(deploy) {
		u.releaseTestLab(ctx, deploy.ID)
		return nil
	}
	if gate, ok := u.testLabGate.(eventTestLabGate); ok {
		return gate.ReleaseEventTestLab(ctx, *deploy.EventID, deploy.ID)
	}
	return calendarError()
}
func (u *ExerciseUseCase) requireScopedPreviewActive(ctx context.Context, deploy exerciseModel.TestDeploy) error {
	if !legacyEventPreview(deploy) {
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
