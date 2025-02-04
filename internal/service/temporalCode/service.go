package temporalCodeService

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/temporalCode"
	"github.com/cybericebox/daemon/internal/tools"
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
		DeleteTemporalCode(ctx context.Context, id uuid.UUID) (int64, error)

		DeleteExpiredTemporalCodes(ctx context.Context) (int64, error)
	}

	Dependencies struct {
		Config     *config.TemporalCodeConfig
		Repository IRepository
	}
)

func NewService(deps Dependencies) *TemporalCodeService {
	return &TemporalCodeService{
		repository: deps.Repository,
		ttl:        deps.Config.TTL,
	}
}

func (s *TemporalCodeService) CreateTemporalContinueRegistrationCode(ctx context.Context, data temporalCodeModel.TemporalContinueRegistrationCodeData) (string, error) {
	return s.createTemporalCode(ctx, temporalCodeModel.ContinueRegistrationCodeType, data)
}

func (s *TemporalCodeService) GetTemporalContinueRegistrationCodeData(ctx context.Context, code string) (*temporalCodeModel.TemporalContinueRegistrationCodeData, error) {
	codeData, err := s.getTemporalCodeData(ctx, code, temporalCodeModel.ContinueRegistrationCodeType)
	if err != nil {
		return nil, temporalCodeModel.ErrTemporalCode.WithError(err).WithMessage("Failed to get temporal code data").Err()
	}

	// unmarshal data
	data := temporalCodeModel.TemporalContinueRegistrationCodeData{}
	if err = json.Unmarshal(codeData, &data); err != nil {
		return nil, temporalCodeModel.ErrTemporalCode.WithError(err).WithMessage("Failed to unmarshal data").Err()
	}

	return &data, nil
}

func (s *TemporalCodeService) CreateTemporalPasswordResettingCode(ctx context.Context, data temporalCodeModel.TemporalPasswordResettingCodeData) (string, error) {
	return s.createTemporalCode(ctx, temporalCodeModel.PasswordResettingCodeType, data)
}

func (s *TemporalCodeService) GetTemporalPasswordResettingCodeData(ctx context.Context, code string) (*temporalCodeModel.TemporalPasswordResettingCodeData, error) {
	codeData, err := s.getTemporalCodeData(ctx, code, temporalCodeModel.PasswordResettingCodeType)
	if err != nil {
		return nil, temporalCodeModel.ErrTemporalCode.WithError(err).WithMessage("Failed to get temporal code data").Err()
	}

	// unmarshal data
	data := temporalCodeModel.TemporalPasswordResettingCodeData{}
	if err = json.Unmarshal(codeData, &data); err != nil {
		return nil, temporalCodeModel.ErrTemporalCode.WithError(err).WithMessage("Failed to unmarshal data").Err()
	}

	return &data, nil
}

func (s *TemporalCodeService) CreateTemporalEmailConfirmationCode(ctx context.Context, data temporalCodeModel.TemporalEmailConfirmationCodeData) (string, error) {
	return s.createTemporalCode(ctx, temporalCodeModel.EmailConfirmationCodeType, data)
}

func (s *TemporalCodeService) GetTemporalEmailConfirmationCodeData(ctx context.Context, code string) (*temporalCodeModel.TemporalEmailConfirmationCodeData, error) {
	codeData, err := s.getTemporalCodeData(ctx, code, temporalCodeModel.EmailConfirmationCodeType)
	if err != nil {
		return nil, temporalCodeModel.ErrTemporalCode.WithError(err).WithMessage("Failed to get temporal code data").Err()
	}

	// unmarshal data
	data := temporalCodeModel.TemporalEmailConfirmationCodeData{}
	if err = json.Unmarshal(codeData, &data); err != nil {
		return nil, temporalCodeModel.ErrTemporalCode.WithError(err).WithMessage("Failed to unmarshal data").Err()
	}

	return &data, nil
}

func (s *TemporalCodeService) createTemporalCode(ctx context.Context, codeType int32, data interface{}) (string, error) {
	baseError := temporalCodeModel.ErrTemporalCode.WithContext("codeType", codeType)

	id := uuid.Must(uuid.NewV7())

	jData, err := json.Marshal(data)
	if err != nil {
		return "", baseError.WithError(err).WithMessage("Failed to marshal data").Err()
	}

	if err = s.repository.CreateTemporalCode(ctx, postgres.CreateTemporalCodeParams{
		ID:        id,
		ExpiredAt: time.Now().Add(s.ttl),
		CodeType:  codeType,
		Data:      jData,
	}); err != nil {
		return "", baseError.WithError(err).WithMessage("Failed to create temporal code").Err()
	}
	return id.String(), nil
}

func (s *TemporalCodeService) getTemporalCodeData(ctx context.Context, code string, codeType int32) (json.RawMessage, error) {
	baseError := temporalCodeModel.ErrTemporalCode.WithContext("codeType", codeType)
	baseInvalidTemporalCodeError := temporalCodeModel.ErrTemporalCodeInvalidCode.WithContext("codeType", codeType)

	id, err := uuid.FromString(code)
	if err != nil {
		return nil, baseInvalidTemporalCodeError.WithMessage("Failed to parse temporal code id").Err()
	}
	temporalCode, err := s.repository.GetTemporalCode(ctx, id)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, baseInvalidTemporalCodeError.Err()
		}
		return nil, baseError.WithError(err).WithMessage("Failed to get temporal code").Err()
	}

	// delete temporal code
	affected, err := s.repository.DeleteTemporalCode(ctx, id)
	if err != nil {
		return nil, baseError.WithError(err).WithMessage("Failed to delete temporal code").Err()
	}

	if affected == 0 {
		return nil, temporalCodeModel.ErrTemporalCodeNotFound.WithContext("codeType", codeType).Err()
	}

	if temporalCode.CodeType != codeType {
		return nil, baseInvalidTemporalCodeError.Err()
	}

	// check if the code is expired
	if time.Now().After(temporalCode.ExpiredAt) {
		return nil, temporalCodeModel.ErrTemporalCodeExpired.WithContext("codeType", codeType).Err()
	}

	return temporalCode.Data, nil
}

func (s *TemporalCodeService) DeleteExpiredTemporalCodes(ctx context.Context) error {
	if _, err := s.repository.DeleteExpiredTemporalCodes(ctx); err != nil {
		return temporalCodeModel.ErrTemporalCode.WithError(err).WithMessage("Failed to delete expired temporal codes").Err()
	}
	return nil
}
