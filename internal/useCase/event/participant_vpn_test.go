package event

import (
	"context"
	"errors"
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
