package temporalCode

import (
	"context"
	"database/sql"
	"errors"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
	"time"
)

type (
	TemporalCodeService struct {
		repository IRepository
		ttl        time.Duration
	}

	IRepository interface {
		GetTemporalCode(ctx context.Context, id uuid.UUID) (postgres.TemporalCode, error)
		CreateTemporalCode(ctx context.Context, arg postgres.CreateTemporalCodeParams) error
		DeleteTemporalCode(ctx context.Context, id uuid.UUID) error
	}

	Dependencies struct {
		Config     *config.TemporalCodeConfig
		Repository IRepository
	}
)

func NewTemporalCodeService(deps Dependencies) *TemporalCodeService {
	return &TemporalCodeService{
		repository: deps.Repository,
		ttl:        deps.Config.TTL,
	}
}

func (s *TemporalCodeService) CreateTemporalContinueRegistrationCode(ctx context.Context, data model.TemporalContinueRegistrationCodeData) (string, error) {
	id := uuid.Must(uuid.NewV7())

	if err := s.repository.CreateTemporalCode(ctx, postgres.CreateTemporalCodeParams{
		ID:        id,
		ExpiredAt: time.Now().Add(s.ttl),
		CodeType:  model.ContinueRegistrationCodeType,
		V0:        data.Email,
		V1:        data.Role,
	}); err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to create temporal code")
	}
	return id.String(), nil
}

func (s *TemporalCodeService) GetTemporalContinueRegistrationCodeData(ctx context.Context, code string) (*model.TemporalContinueRegistrationCodeData, error) {
	id, err := uuid.FromString(code)
	if err != nil {
		return nil, model.ErrInvalidTemporalCode.WithError(appError.NewError().WithError(err).WithMessage("failed to parse temporal code id"))
	}
	temporalCode, err := s.repository.GetTemporalCode(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrInvalidTemporalCode
		}
		return nil, appError.NewError().WithError(err).WithMessage("failed to get temporal code")
	}

	// delete temporal code
	if err = s.repository.DeleteTemporalCode(ctx, id); err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to delete temporal code")
	}

	if temporalCode.CodeType != model.ContinueRegistrationCodeType || temporalCode.ExpiredAt.Before(time.Now()) {
		return nil, model.ErrInvalidTemporalCode
	}

	return &model.TemporalContinueRegistrationCodeData{
		Email: temporalCode.V0,
		Role:  temporalCode.V1,
	}, nil
}

func (s *TemporalCodeService) CreateTemporalPasswordResettingCode(ctx context.Context, data model.TemporalPasswordResettingCodeData) (string, error) {
	id := uuid.Must(uuid.NewV7())

	if err := s.repository.CreateTemporalCode(ctx, postgres.CreateTemporalCodeParams{
		ID:        id,
		ExpiredAt: time.Now().Add(s.ttl),
		CodeType:  model.PasswordResettingCodeType,
		V0:        data.UserID.String(),
	}); err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to create temporal code")
	}
	return id.String(), nil
}

func (s *TemporalCodeService) GetTemporalPasswordResettingCodeData(ctx context.Context, code string) (*model.TemporalPasswordResettingCodeData, error) {
	id, err := uuid.FromString(code)
	if err != nil {
		return nil, model.ErrInvalidTemporalCode.WithError(appError.NewError().WithError(err).WithMessage("failed to parse temporal code id"))
	}
	temporalCode, err := s.repository.GetTemporalCode(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrInvalidTemporalCode
		}
		return nil, appError.NewError().WithError(err).WithMessage("failed to get temporal code")
	}

	// delete temporal code
	if err = s.repository.DeleteTemporalCode(ctx, id); err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to delete temporal code")
	}

	if temporalCode.CodeType != model.PasswordResettingCodeType || temporalCode.ExpiredAt.Before(time.Now()) {
		return nil, model.ErrInvalidTemporalCode
	}

	userID, err := uuid.FromString(temporalCode.V0)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to parse user id")
	}

	return &model.TemporalPasswordResettingCodeData{
		UserID: userID,
	}, nil
}

func (s *TemporalCodeService) CreateTemporalEmailConfirmationCode(ctx context.Context, data model.TemporalEmailConfirmationCodeData) (string, error) {
	id := uuid.Must(uuid.NewV7())

	if err := s.repository.CreateTemporalCode(ctx, postgres.CreateTemporalCodeParams{
		ID:        id,
		ExpiredAt: time.Now().Add(s.ttl),
		CodeType:  model.EmailConfirmationCodeType,
		V0:        data.UserID.String(),
		V1:        data.Email,
	}); err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to create temporal code")
	}
	return id.String(), nil
}

func (s *TemporalCodeService) GetTemporalEmailConfirmationCodeData(ctx context.Context, code string) (*model.TemporalEmailConfirmationCodeData, error) {
	id, err := uuid.FromString(code)
	if err != nil {
		return nil, model.ErrInvalidTemporalCode.WithError(appError.NewError().WithError(err).WithMessage("failed to parse temporal code id"))
	}
	temporalCode, err := s.repository.GetTemporalCode(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrInvalidTemporalCode
		}
		return nil, appError.NewError().WithError(err).WithMessage("failed to get temporal code")
	}

	// delete temporal code
	if err = s.repository.DeleteTemporalCode(ctx, id); err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to delete temporal code")
	}

	if temporalCode.CodeType != model.EmailConfirmationCodeType || temporalCode.ExpiredAt.Before(time.Now()) {
		return nil, model.ErrInvalidTemporalCode
	}

	userID, err := uuid.FromString(temporalCode.V0)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to parse user id")
	}

	return &model.TemporalEmailConfirmationCodeData{
		UserID: userID,
		Email:  temporalCode.V1,
	}, nil
}
