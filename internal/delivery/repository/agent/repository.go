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
		client client.AgentClient
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

func newAgent(cfg *config.AgentGRPCConfig) (client.AgentClient, error) {
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
		return nil, model.ErrAgent.WithError(err).WithMessage("Failed to create agent client").Err()
	}

	if _, err = c.Ping(context.Background(), &protobuf.EmptyRequest{}); err != nil {
		return nil, model.ErrAgent.WithError(err).WithMessage("Failed to ping agent").Err()
	}

	return c, nil
}

func (r *AgentRepository) Close() {
	if err := r.client.Close(); err != nil {
		log.Error().Err(err).Msg("Failed to close agent repository")
	}
}

// laboratories

func (r *AgentRepository) GetLaboratories(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LaboratoryInfo, error) {
	srtLabIDs := make([]string, 0)

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	resp, err := r.client.GetLabs(ctx, &protobuf.LabsRequest{IDs: srtLabIDs})
	if err != nil {
		return nil, model.ErrAgent.WithError(err).WithMessage("Failed to get labs").Err()
	}

	var errs error
	labsInfo := make([]*model.LaboratoryInfo, 0, len(resp.GetLabs()))
	for _, l := range resp.GetLabs() {
		id, err := uuid.FromString(l.GetID())
		if err != nil {
			errs = multierror.Append(errs, model.ErrAgent.WithError(err).WithMessage("Failed to parse lab id").WithContext("lab_id", l.GetID()).Err())
		}
		labsInfo = append(labsInfo, &model.LaboratoryInfo{
			ID:   id,
			CIDR: l.GetCIDR(),
		})
	}

	if errs != nil {
		return nil, errs
	}

	return labsInfo, nil
}

func (r *AgentRepository) CreateLaboratories(ctx context.Context, mask, count int) ([]uuid.UUID, error) {
	resp, err := r.client.CreateLabs(ctx, &protobuf.CreateLabsRequest{CIDRMask: uint32(mask), Count: uint32(count)})
	if err != nil {
		return nil, model.ErrAgent.WithError(err).WithMessage("Failed to create labs").WithContext("mask", mask).WithContext("count", count).Err()
	}

	labIDs := make([]uuid.UUID, 0, len(resp.GetLabs()))

	for _, lab := range resp.GetLabs() {
		id, err := uuid.FromString(lab.GetID())
		if err != nil {
			return nil, model.ErrAgent.WithError(err).WithMessage("Failed to parse lab id").WithContext("lab_id", id).Err()
		}
		labIDs = append(labIDs, id)
	}

	return labIDs, nil
}

func (r *AgentRepository) StartLaboratories(ctx context.Context, labIDs ...uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	if _, err := r.client.StartLabs(ctx, &protobuf.LabsRequest{IDs: srtLabIDs}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to start labs").WithContext("lab_ids", srtLabIDs).Err()
	}
	return nil
}

func (r *AgentRepository) StopLaboratories(ctx context.Context, labIDs ...uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	if _, err := r.client.StopLabs(ctx, &protobuf.LabsRequest{IDs: srtLabIDs}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to stop labs").WithContext("lab_ids", srtLabIDs).Err()
	}
	return nil
}

func (r *AgentRepository) DeleteLaboratories(ctx context.Context, labIDs ...uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	if _, err := r.client.DeleteLabs(ctx, &protobuf.LabsRequest{IDs: srtLabIDs}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to delete labs").WithContext("lab_ids", srtLabIDs).Err()
	}
	return nil
}

// laboratory challenges

func (r *AgentRepository) AddLaboratoriesChallenges(ctx context.Context, labIDs []uuid.UUID, configs []model.LaboratoryChallenge, flagEnvVariables []model.FlagVariable) error {
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
				ID:    i.ID.String(),
				Image: i.Image,
				Resources: &protobuf.Resources{
					Memory: 50 * 1024 * 1024, //TODO: make it configurable (50Mi)
					CPU:    5,                //TODO: make it configurable (5m)
				},
				Envs:    envs,
				Records: records,
			})
		}

		challenges = append(challenges, &protobuf.Challenge{
			ID:        c.ID.String(),
			Instances: instances,
		})
	}

	srtLabIDs := make([]string, 0, len(labIDs))
	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	flagEnvVars := make([]*protobuf.FlagEnvVariable, 0, len(flagEnvVariables))
	for _, f := range flagEnvVariables {
		flagEnvVars = append(flagEnvVars, &protobuf.FlagEnvVariable{
			LabID:       f.LabID.String(),
			ChallengeID: f.ChallengeID.String(),
			InstanceID:  f.InstanceID.String(),
			Variable:    f.Variable,
			Flag:        f.Flag,
		})
	}

	if _, err := r.client.AddLabsChallenges(ctx, &protobuf.AddLabsChallengesRequest{
		LabIDs:           srtLabIDs,
		Challenges:       challenges,
		FlagEnvVariables: flagEnvVars,
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to add lab challenges").WithContext("labIDs", srtLabIDs).WithContext("challenges", challenges).Err()
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

	if _, err := r.client.DeleteLabsChallenges(ctx, &protobuf.LabsChallengesRequest{
		LabIDs:       srtLabIDs,
		ChallengeIDs: srtChallengeIDs,
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to delete lab challenges").WithContext("lab_ids", srtLabIDs).WithContext("challenge_ids", srtChallengeIDs).Err()
	}
	return nil
}

func (r *AgentRepository) StartLaboratoriesChallenges(ctx context.Context, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))
	srtChallengeIDs := make([]string, 0, len(challengeIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	for _, c := range challengeIDs {
		srtChallengeIDs = append(srtChallengeIDs, c.String())
	}

	if _, err := r.client.StartLabsChallenges(ctx, &protobuf.LabsChallengesRequest{
		LabIDs:       srtLabIDs,
		ChallengeIDs: srtChallengeIDs,
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to start lab challenges").WithContext("lab_ids", srtLabIDs).WithContext("challenge_ids", srtChallengeIDs).Err()
	}
	return nil
}

func (r *AgentRepository) StopLaboratoriesChallenges(ctx context.Context, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))
	srtChallengeIDs := make([]string, 0, len(challengeIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	for _, c := range challengeIDs {
		srtChallengeIDs = append(srtChallengeIDs, c.String())
	}

	if _, err := r.client.StopLabsChallenges(ctx, &protobuf.LabsChallengesRequest{
		LabIDs:       srtLabIDs,
		ChallengeIDs: srtChallengeIDs,
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to stop lab challenges").WithContext("lab_ids", srtLabIDs).WithContext("challenge_ids", srtChallengeIDs).Err()
	}
	return nil
}

func (r *AgentRepository) ResetLaboratoriesChallenges(ctx context.Context, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	srtLabIDs := make([]string, 0, len(labIDs))
	srtChallengeIDs := make([]string, 0, len(challengeIDs))

	for _, l := range labIDs {
		srtLabIDs = append(srtLabIDs, l.String())
	}

	for _, c := range challengeIDs {
		srtChallengeIDs = append(srtChallengeIDs, c.String())
	}

	if _, err := r.client.StopLabsChallenges(ctx, &protobuf.LabsChallengesRequest{
		LabIDs:       srtLabIDs,
		ChallengeIDs: srtChallengeIDs,
	}); err != nil {
		return model.ErrAgent.WithError(err).WithMessage("Failed to reset lab challenges").WithContext("lab_ids", srtLabIDs).WithContext("challenge_ids", srtChallengeIDs).Err()
	}
	return nil
}
