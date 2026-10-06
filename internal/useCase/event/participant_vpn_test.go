package event

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
)

type participantVPNStoreStub struct{ config string }

func (s *participantVPNStoreStub) StoreConfig(context.Context, uuid.UUID, vpnModel.Scope, uuid.NullUUID, string) error {
	return nil
}
func (s *participantVPNStoreStub) GetConfig(context.Context, uuid.UUID, vpnModel.Scope, uuid.NullUUID) (string, error) {
	return s.config, nil
}

type participantVPNInfraStub struct{ issueCalls int }

func (s *participantVPNInfraStub) DeployLab(context.Context, string, string, infraModel.LabMeta, exerciseModel.Topology) error {
	return nil
}
func (s *participantVPNInfraStub) LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error) {
	return exerciseModel.LabDeployStatus{}, nil
}
func (s *participantVPNInfraStub) EnsureLabClient(context.Context, string, string) (string, error) {
	s.issueCalls++
	return "new-config", nil
}
func (*participantVPNInfraStub) EnsureVPNGroup(context.Context, string) error  { return nil }
func (*participantVPNInfraStub) DestroyLabGroup(context.Context, string) error { return nil }

func TestEnsureParticipantVPNConfigUsesStoredConfigBeforeAgent(t *testing.T) {
	store := &participantVPNStoreStub{config: "stored-config"}
	infra := &participantVPNInfraStub{}

	config, err := ensureParticipantVPNConfig(context.Background(), store, infra, "group", uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatalf("ensureParticipantVPNConfig: %v", err)
	}
	if config != "stored-config" {
		t.Fatalf("config = %q, want stored config", config)
	}
	if infra.issueCalls != 0 {
		t.Fatalf("agent issue calls = %d, want 0", infra.issueCalls)
	}
}

type terminatingClientInfra struct{ participantVPNInfraStub }

func (*terminatingClientInfra) EnsureLabClient(context.Context, string, string) (string, error) {
	return "", &infraModel.TerminatingError{Message: "LabGroupClient is still being deleted", RetryAfter: time.Second}
}

func TestEnsureParticipantVPNConfigTerminatingIsFriendlyRetry(t *testing.T) {
	_, err := ensureParticipantVPNConfig(context.Background(), &participantVPNStoreStub{}, &terminatingClientInfra{}, "group", uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()))
	if !errors.Is(err, infraModel.ErrLabAccessRetry.Err()) {
		t.Fatalf("want ErrLabAccessRetry, got %v", err)
	}
}

type racingVPNStore struct {
	mu     sync.Mutex
	config string
}

func (s *racingVPNStore) StoreConfig(_ context.Context, _ uuid.UUID, _ vpnModel.Scope, _ uuid.NullUUID, config string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.config = config
	return nil
}
func (s *racingVPNStore) GetConfig(context.Context, uuid.UUID, vpnModel.Scope, uuid.NullUUID) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.config, nil
}

type slowIssueInfra struct {
	participantVPNInfraStub
	calls atomic.Int32
}

func (s *slowIssueInfra) EnsureLabClient(context.Context, string, string) (string, error) {
	s.calls.Add(1)
	time.Sleep(50 * time.Millisecond)
	return "new-config", nil
}

func TestEnsureParticipantVPNConfigConcurrentSyncsCreateOneClient(t *testing.T) {
	store, infra := &racingVPNStore{}, &slowIssueInfra{}
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if config, err := ensureParticipantVPNConfig(context.Background(), store, infra, "group", eventID, userID); err != nil || config != "new-config" {
				t.Errorf("config=%q err=%v", config, err)
			}
		}()
	}
	wg.Wait()
	if got := infra.calls.Load(); got != 1 {
		t.Fatalf("agent issue calls = %d, want 1", got)
	}
}
