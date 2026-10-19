package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// reservationAgent is the stand agent that also says which agent holds a group (every group is on one agent).
type reservationAgent struct {
	*standAgent
	id uuid.UUID
}

func (a reservationAgent) GroupAgent(context.Context, string) (uuid.UUID, bool, error) {
	return a.id, true, nil
}

// A part that ends while the event runs: the cluster deleted the team groups and every lab in them. Their lifecycle
// is settled as ended with the reservation: the lifecycle pass neither starts nor stops anything nor fails, the
// stand pass writes no failed binding and no failed-lab signal, and the labs hold nothing.
func TestMidEventPartEndSettlesLabsWithoutFailure(t *testing.T) {
	f, _, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	agent := reservationAgent{standAgent: f.agent, id: uuid.Must(uuid.NewV7())}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)),
		Infra: agent, InfrastructureCapability: standCapability{}, Topologies: f.topologies,
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now), StandDeployBudget: 200,
	})
	uc.SetLifecycleControls(true)
	rows, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	if err != nil || len(rows) == 0 {
		t.Fatal("fixture labs", len(rows), err)
	}
	// The cluster deleted everything under the part: the agent no longer knows the labs.
	f.agent.mu.Lock()
	f.agent.observations = map[eventLabModel.Ref]eventLabModel.Observation{}
	f.agent.ready = map[string]bool{}
	stops := len(f.agent.stopCalls)
	f.agent.mu.Unlock()

	if err = uc.ReservationPartEnded(ctx, f.eventID, agent.id, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	labs := eventLabRepo.New(f.db.Queries)
	for _, row := range rows {
		l, getErr := labs.Get(ctx, row.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if !l.EndedWithReservation() || l.HeldCompute() != (eventLabModel.Compute{}) || l.FailureCode != "" {
			t.Fatalf("lab %s not settled as ended: %+v", l.ID, l)
		}
	}
	dirty, err := labs.ListDirty(ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range dirty {
		if l.EventID == f.eventID {
			t.Fatalf("an ended lab is still lifecycle work: %+v", l)
		}
	}
	// The passes after the end: no start (the fake refuses one), no stop, no failure.
	if err = uc.ReconcilePendingLabLifecycles(ctx); err != nil {
		t.Fatalf("lifecycle pass after the end: %v", err)
	}
	if err = uc.ReconcileEventStands(ctx); err != nil {
		t.Fatalf("stand pass after the end: %v", err)
	}
	f.agent.mu.Lock()
	if len(f.agent.stopCalls) != stops {
		t.Fatalf("stopped an ended lab: %+v", f.agent.stopCalls[stops:])
	}
	f.agent.mu.Unlock()
	var failed, signals int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM lab_bindings WHERE event_id=$1 AND readiness=$2`, f.eventID, int16(labBindingModel.ReadinessFailed)).Scan(&failed); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM signal_outbox WHERE signal_type=$1`, string(signalModel.TypeEventLabFailed)).Scan(&signals); err != nil {
		t.Fatal(err)
	}
	if failed != 0 || signals != 0 {
		t.Fatalf("the end reads as a failure: %d failed bindings, %d failed-lab signals", failed, signals)
	}
	var destroyed int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM lab_bindings WHERE event_id=$1 AND readiness=$2`, f.eventID, int16(labBindingModel.ReadinessDestroyed)).Scan(&destroyed); err != nil || destroyed == 0 {
		t.Fatalf("bindings not recorded as removed: %d %v", destroyed, err)
	}
	// Repeating the end changes nothing.
	if err = uc.ReservationPartEnded(ctx, f.eventID, agent.id, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
