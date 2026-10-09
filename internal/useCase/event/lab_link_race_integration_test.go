package event_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/labaccess"
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

type webStatusAgent struct{ *standAgent }

func (a webStatusAgent) LabStatus(_ context.Context, group, lab string) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{LabUID: "uid-" + group + "/" + lab, LabGeneration: 1, Ready: true, Phase: "Ready", Access: []exerciseModel.LabAccess{{Device: "web", Port: 80, Protocol: "http", URL: "https://web.example"}}}, nil
}

type waitingIssuer struct{ entered, release chan struct{} }

func (s *waitingIssuer) Issue(ctx context.Context, _ labaccess.Session, _ time.Time) (labaccess.Link, error) {
	close(s.entered)
	select {
	case <-s.release:
		return labaccess.Link{URL: "https://web.example/_auth?t=secret"}, nil
	case <-ctx.Done():
		return labaccess.Link{}, ctx.Err()
	}
}
func TestLateLinkAfterFinalSolveRejected(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	issuer := &waitingIssuer{make(chan struct{}), make(chan struct{})}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: webStatusAgent{f.agent}, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets}, LabSessions: issuer, VPN: &recordingVPNStore{config: "stored"}})
	for i, ch := range set.challenges[:2] {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	type result struct {
		link labaccess.Link
		err  error
	}
	done := make(chan result, 1)
	go func() {
		link, err := uc.OpenOwnLabLink(ctx, f.eventID, user, set.challenges[0], "web", 80)
		done <- result{link, err}
	}()
	select {
	case <-issuer.entered:
	case <-ctx.Done():
		t.Fatal("link never reached issuer", ctx.Err())
	}
	// The answer commits while signing is pending, proving no DB locks cross it.
	submitStoredFlag(t, f, user, set.challenges[2], at.Add(2*time.Second))
	close(issuer.release)
	select {
	case got := <-done:
		if got.err == nil || got.link.URL != "" {
			t.Fatalf("late link leaked %+v %v", got.link, got.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestLateLinkAfterCanonicalBindingChangeRejected(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	issuer := &waitingIssuer{make(chan struct{}), make(chan struct{})}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: webStatusAgent{f.agent}, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets}, LabSessions: issuer, VPN: &recordingVPNStore{config: "stored"}})
	_ = at
	type result struct {
		link labaccess.Link
		err  error
	}
	done := make(chan result, 1)
	go func() {
		link, err := uc.OpenOwnLabLink(ctx, f.eventID, user, set.challenges[0], "web", 80)
		done <- result{link, err}
	}()
	select {
	case <-issuer.entered:
	case <-ctx.Done():
		t.Fatal("link never reached issuer", ctx.Err())
	}
	// Source recreation changed the canonical binding while signing was pending.
	if _, err := f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET lab_id=NULL,generation=generation+1 WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[0]); err != nil {
		t.Fatal(err)
	}
	close(issuer.release)
	select {
	case got := <-done:
		if got.err == nil || got.link.URL != "" {
			t.Fatalf("late link leaked %+v %v", got.link, got.err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
func TestAllowedLabLinkContainsCanonicalClientFence(t *testing.T) {
	f, set, user, _ := prepareLifecycle(t)
	sessions := &recordingSessions{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), Infra: webStatusAgent{f.agent}, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets}, LabSessions: sessions, VPN: &recordingVPNStore{config: "stored"}})
	link, err := uc.OpenOwnLabLink(context.Background(), f.eventID, user, set.challenges[0], "web", 80)
	if err != nil || link.URL == "" || link.LabID == uuid.Nil || link.Revision != "1" {
		t.Fatalf("link fence %+v %v", link, err)
	}
}
