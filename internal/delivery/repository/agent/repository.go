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
		log.Fatal().Err(err).Msg("Failed to create agent repository")
		return nil
	}

	return &AgentRepository{
		cl,
	}
}

func newAgent(cfg *config.AgentGRPCConfig) (protobuf.AgentClient, error) {
	c, err := client.NewAgentConnection(client.Config{
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

	if err != nil {
		return nil, model.ErrAgent.WithError(err).WithMessage("Failed to create agent client").Cause()
	}

	if _, err = c.Ping(context.Background(), &protobuf.EmptyRequest{}); err != nil {
		return nil, model.ErrAgent.WithError(err).WithMessage("Failed to ping agent").Cause()
	}

	return c, nil
}

func (r *AgentRepository) GetLaboratories(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LaboratoryInfo, error) {
	srtLabIDs := make([]string, 0)

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	resp, err := r.AgentClient.GetLabs(ctx, &protobuf.GetLabsRequest{Ids: srtLabIDs})
	if err != nil {
		return nil, model.ErrAgent.WithError(err).WithMessage("Failed to get labs").Cause()
	}

	var errs error
	labsInfo := make([]*model.LaboratoryInfo, 0, len(resp.GetLabs()))
	for _, l := range resp.GetLabs() {
		id, err := uuid.FromString(l.GetId())
		if err != nil {
			errs = multierror.Append(errs, model.ErrAgent.WithError(err).WithMessage("Failed to parse lab id").WithContext("lab_id", l.GetId()).Cause())
		}
		labsInfo = append(labsInfo, &model.LaboratoryInfo{
			ID:   id,
			CIDR: l.GetCidr(),
		})
	}

	if errs != nil {
		return nil, errs
	}

	return labsInfo, nil
}

func (r *AgentRepository) CreateLaboratories(ctx context.Context, mask, count int) ([]uuid.UUID, error) {
	resp, err := r.AgentClient.CreateLabs(ctx, &protobuf.CreateLabsRequest{CidrMask: uint32(mask), Count: uint32(count)})
	if err != nil {
		return nil, model.ErrAgent.WithError(err).WithMessage("Failed to create labs").WithContext("mask", mask).WithContext("count", count).Cause()
	}

	labIDs := make([]uuid.UUID, 0, len(resp.GetIds()))

	for _, id := range resp.GetIds() {
		id, err := uuid.FromString(id)
		if err != nil {
			return nil, model.ErrAgent.WithError(err).WithMessage("Failed to parse lab id").WithContext("lab_id", id).Cause()
		}
		labIDs = append(labIDs, id)
	}

	return labIDs, nil
}

func (r *AgentRepository) DeleteLaboratories(ctx context.Context, labIDs ...uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	if _, err := r.AgentClient.DeleteLabs(ctx, &protobuf.DeleteLabsRequest{Ids: srtLabIDs}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to delete labs").WithContext("lab_ids", srtLabIDs).Cause()
	}
	return nil
}

func (r *AgentRepository) AddLaboratoryChallenges(ctx context.Context, labID uuid.UUID, configs []model.LaboratoryChallenge) error {
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
					Memory: "50Mi", //TODO: make it configurable
					Cpu:    "5m",   //TODO: make it configurable
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

	if _, err := r.AgentClient.AddLabChallenges(ctx, &protobuf.AddLabChallengesRequest{
		LabID:      labID.String(),
		Challenges: challenges,
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to add lab challenges").WithContext("lab_id", labID).WithContext("challenges", challenges).Cause()
	}
	return nil
}

func (r *AgentRepository) DeleteLaboratoriesChallenges(ctx context.Context, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))
	srtChallengeIDs := make([]string, 0, len(challengeIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	for _, c := range challengeIDs {
		srtChallengeIDs = append(srtChallengeIDs, c.String())
	}

	if _, err := r.AgentClient.DeleteLabsChallenges(ctx, &protobuf.DeleteLabsChallengesRequest{
		LabIDs:       srtLabIDs,
		ChallengeIDs: srtChallengeIDs,
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to delete lab challenges").WithContext("lab_ids", srtLabIDs).WithContext("challenge_ids", srtChallengeIDs).Cause()
	}
	return nil
}

func (r *AgentRepository) StartChallenge(ctx context.Context, labID, challengeID uuid.UUID) error {
	if _, err := r.AgentClient.StartChallenge(ctx, &protobuf.ChallengeRequest{
		LabID: labID.String(),
		Id:    challengeID.String(),
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to start challenge").WithContext("lab_id", labID).WithContext("challenge_id", challengeID).Cause()
	}
	return nil
}

func (r *AgentRepository) StopChallenge(ctx context.Context, labID, challengeID uuid.UUID) error {
	if _, err := r.AgentClient.StopChallenge(ctx, &protobuf.ChallengeRequest{
		LabID: labID.String(),
		Id:    challengeID.String(),
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to stop challenge").WithContext("lab_id", labID).WithContext("challenge_id", challengeID).Cause()
	}
	return nil
}

func (r *AgentRepository) ResetChallenge(ctx context.Context, labID, challengeID uuid.UUID) error {
	if _, err := r.AgentClient.ResetChallenge(ctx, &protobuf.ChallengeRequest{
		LabID: labID.String(),
		Id:    challengeID.String(),
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to reset challenge").WithContext("lab_id", labID).WithContext("challenge_id", challengeID).Cause()
	}
	return nil
}
