package resourceCalendarUseCase

import (
	"context"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"sync"
	"testing"
	"time"
)

type serializedPreviewTx struct {
	mu sync.Mutex
	s  Store
}

func (t *serializedPreviewTx) Do(ctx context.Context, fn func(context.Context, Store) error) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return fn(ctx, t.s)
}
func eventPreviewHarness(t *testing.T) (*harness, *calModel.Reservation) {
	h := newHarness(t)
	id := uuid.Must(uuid.NewV7())
	r := &calModel.Reservation{ID: id, Kind: calModel.KindEvent, EventID: &h.event.ID, Window: calModel.Window{Start: now0.Add(-time.Hour), End: now0.Add(time.Hour)}, Size: Amount{CPUMillicores: 1000, MemoryBytes: 1000}, Placement: []calModel.Share{{AgentID: uuid.UUID{15: 1}, Units: 1}}}
	h.store.reservations[id] = r
	h.uc.tx = &serializedPreviewTx{s: h.store}
	h.uc.overhead = func() Amount { return Amount{CPUMillicores: 100, MemoryBytes: 100} }
	h.usage.u = Usage{ByEvent: map[uuid.UUID]Amount{h.event.ID: {CPUMillicores: 600, MemoryBytes: 600}}}
	return h, r
}
func previewReq(h *harness, owner uuid.UUID) EventTestLabRequest {
	return EventTestLabRequest{EventID: h.event.ID, Group: "preview-" + owner.String(), TestLabRequest: TestLabRequest{ID: uuid.Must(uuid.NewV7()), Owner: owner, Size: Amount{CPUMillicores: 200, MemoryBytes: 200}, Lease: 2 * time.Hour}}
}
func TestEventPreviewAdmissionNeverUsesAuthorCapacity(t *testing.T) {
	for _, kind := range []string{"missing", "before start", "at end", "canceled", "unbounded", "disabled", "overhead exceeds budget"} {
		t.Run(kind, func(t *testing.T) {
			h, r := eventPreviewHarness(t)
			switch kind {
			case "missing":
				delete(h.store.reservations, r.ID)
			case "before start":
				r.Window.Start = now0.Add(time.Second)
			case "at end":
				r.Window.End = now0
			case "canceled":
				r.Cancel(now0)
			case "unbounded":
				r.Window.End = time.Time{}
			case "disabled":
				h.event.InfrastructureAllowed = false
				h.uc.events = fakeEvents{h.event}
			case "overhead exceeds budget":
				r.Size = Amount{CPUMillicores: 850, MemoryBytes: 850}
			}
			_, err := h.uc.AdmitEventTestLab(context.Background(), previewReq(h, uuid.Must(uuid.NewV7())))
			require.Error(t, err)
			require.Empty(t, h.store.holds)
		})
	}
}
func TestEventPreviewAtomicHoldsChargeGroupOnceAndSurviveLeaseUntilCleanup(t *testing.T) {
	h, r := eventPreviewHarness(t)
	r.Size = Amount{CPUMillicores: 1100, MemoryBytes: 1100}
	owner := uuid.Must(uuid.NewV7())
	a, b := previewReq(h, owner), previewReq(h, owner)
	room, err := h.uc.AdmitEventTestLab(context.Background(), a)
	require.NoError(t, err)
	require.Equal(t, r.Window.End, room.ExpiresAt)
	_, err = h.uc.AdmitEventTestLab(context.Background(), b)
	require.NoError(t, err)
	require.Len(t, h.store.holds, 3)
	candidates, candidateErr := h.uc.EventPreviewCleanupCandidates(context.Background())
	require.NoError(t, candidateErr)
	require.Len(t, candidates, 2, "unexpired run holds retain exact cleanup identities; service envelope is not a run")
	_, err = h.uc.AdmitEventTestLab(context.Background(), previewReq(h, owner))
	require.Error(t, err)
	h.clock = now0.Add(2 * time.Hour)
	r.Window.End = h.clock.Add(time.Hour)
	_, err = h.uc.AdmitEventTestLab(context.Background(), previewReq(h, owner))
	require.Error(t, err, "lease expiry cannot credit an infrastructure object before cleanup")
	require.NoError(t, h.uc.ReleaseEventTestLab(context.Background(), h.event.ID, a.ID))
	require.Len(t, h.store.holds, 2)
	require.NoError(t, h.uc.ReleaseEventTestLab(context.Background(), h.event.ID, b.ID))
	require.Empty(t, h.store.holds)
}
func TestEventPreviewConcurrentStartsCannotSpendSameRemainder(t *testing.T) {
	h, _ := eventPreviewHarness(t)
	owner := uuid.Must(uuid.NewV7())
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for range 2 {
		req := previewReq(h, owner)
		wg.Add(1)
		go func() { defer wg.Done(); _, err := h.uc.AdmitEventTestLab(context.Background(), req); out <- err }()
	}
	wg.Wait()
	close(out)
	success := 0
	for err := range out {
		if err == nil {
			success++
		}
	}
	require.Equal(t, 1, success)
	require.Len(t, h.store.holds, 2)
}
