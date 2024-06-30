package agent

import (
	"context"
	"github.com/cybericebox/agent/pkg/controller/grpc/client"
	"github.com/cybericebox/agent/pkg/controller/grpc/protobuf"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog/log"
)

type (
	AgentRepository struct {
		protobuf.AgentClient
	}

	Dependencies struct {
		Config *config.AgentGRPCConfig
	}
)

func NewRepository(deps Dependencies) *AgentRepository {
	cl, err := newAgent(deps.Config)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create agent client")
		return nil
	}

	return &AgentRepository{
		cl,
	}
}

func newAgent(cfg *config.AgentGRPCConfig) (protobuf.AgentClient, error) {
	return client.NewAgentConnection(client.Config{
		Endpoint: cfg.Endpoint,
		Auth: client.Auth{
			AuthKey: cfg.AuthKey,
			SignKey: cfg.SignKey,
		},
		TLS: client.TLS{
			Enabled:  cfg.TLS.Enabled,
			CertFile: cfg.TLS.CertFile,
			CertKey:  cfg.TLS.KeyFile,
		},
	})
}

func (r *AgentRepository) GetLabs(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LabInfo, error) {
	srtLabIDs := make([]string, 0)

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	resp, err := r.AgentClient.GetLabs(ctx, &protobuf.GetLabsRequest{LabIDs: srtLabIDs})
	if err != nil {
		return nil, err
	}

	var errs error
	labsInfo := make([]*model.LabInfo, 0, len(resp.GetLabs()))
	for _, l := range resp.GetLabs() {
		id, err := uuid.FromString(l.GetId())
		if err != nil {
			errs = multierror.Append(errs, err)
		}
		labsInfo = append(labsInfo, &model.LabInfo{
			ID:   id,
			CIDR: l.GetCidr(),
		})
	}

	if errs != nil {
		return nil, errs
	}

	return labsInfo, nil
}

func (r *AgentRepository) CreateLab(ctx context.Context, mask int) (uuid.UUID, error) {
	resp, err := r.AgentClient.CreateLab(ctx, &protobuf.CreateLabRequest{CidrMask: uint32(mask)})
	if err != nil {
		return uuid.Nil, err
	}

	id, err := uuid.FromString(resp.GetId())
	if err != nil {
		return uuid.Nil, err
	}

	return id, nil

}

func (r *AgentRepository) DeleteLabs(ctx context.Context, labIDs ...uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	_, err := r.AgentClient.DeleteLabs(ctx, &protobuf.DeleteLabsRequest{LabIDs: srtLabIDs})
	return err
}

func (r *AgentRepository) AddLabsChallenges(ctx context.Context, labID uuid.UUID, configs []model.LabChallenge) error {

	challenges := make([]*protobuf.Challenge, 0, len(configs))
	for _, c := range configs {
		instances := make([]*protobuf.Instance, 0, len(c.Instances))
		for _, i := range c.Instances {
			records := make([]*protobuf.DNSRecord, 0, len(i.DNSRecords))
			for _, r := range i.DNSRecords {
				records = append(records, &protobuf.DNSRecord{
					Name: r.Name,
					Type: r.Type,
					Data: r.Value,
				})
			}

			envs := make([]*protobuf.EnvVariable, 0, len(i.EnvVars))
			for _, e := range i.EnvVars {
				envs = append(envs, &protobuf.EnvVariable{
					Name:  e.Name,
					Value: e.Value,
				})
			}

			instances = append(instances, &protobuf.Instance{
				Id:    i.ID.String(),
				Image: i.Image,
				Resources: &protobuf.Resources{
					Memory: "50Mi",
					Cpu:    "300m",
				},
				Envs:    envs,
				Records: records,
			})
		}

		challenges = append(challenges, &protobuf.Challenge{
			Id:        c.ID.String(),
			Instances: instances,
		})
	}

	_, err := r.AgentClient.AddLabsChallenges(ctx, &protobuf.AddLabsChallengesRequest{
		LabIDs:     []string{labID.String()},
		Challenges: challenges,
	})
	return err
}

func (r *AgentRepository) DeleteLabsChallenges(ctx context.Context, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))
	srtChallengeIDs := make([]string, 0, len(challengeIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	for _, c := range challengeIDs {
		srtChallengeIDs = append(srtChallengeIDs, c.String())
	}

	_, err := r.AgentClient.DeleteLabsChallenges(ctx, &protobuf.DeleteLabsChallengesRequest{
		LabIDs:       srtLabIDs,
		ChallengeIDs: srtChallengeIDs,
	})
	return err
}
