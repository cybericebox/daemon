package temporalCode

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
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

func NewTemporalCodeService(deps Dependencies) *TemporalCodeService {
	return &TemporalCodeService{
		repository: deps.Repository,
		ttl:        deps.Config.TTL,
	}
}

func (s *TemporalCodeService) CreateTemporalContinueRegistrationCode(ctx context.Context, data model.TemporalContinueRegistrationCodeData) (string, error) {
	return s.createTemporalCode(ctx, model.ContinueRegistrationCodeType, data)
}

func (s *TemporalCodeService) GetTemporalContinueRegistrationCodeData(ctx context.Context, code string) (*model.TemporalContinueRegistrationCodeData, error) {
	codeData, err := s.getTemporalCodeData(ctx, code, model.ContinueRegistrationCodeType)
	if err != nil {
		return nil, model.ErrTemporalCode.WithError(err).WithMessage("Failed to get temporal code data").Cause()
	}

	// unmarshal data
	data := model.TemporalContinueRegistrationCodeData{}
	if err = json.Unmarshal(codeData, &data); err != nil {
		return nil, model.ErrTemporalCode.WithError(err).WithMessage("Failed to unmarshal data").Cause()
	}

	return &data, nil
}

func (s *TemporalCodeService) DeleteExpiredTemporalCodes(ctx context.Context) error {
	if _, err := s.repository.DeleteExpiredTemporalCodes(ctx); err != nil {
		return model.ErrTemporalCode.WithError(err).WithMessage("Failed to delete expired temporal codes").Cause()
	}
	return nil
}

func (s *TemporalCodeService) CreateTemporalPasswordResettingCode(ctx context.Context, data model.TemporalPasswordResettingCodeData) (string, error) {
	return s.createTemporalCode(ctx, model.PasswordResettingCodeType, data)
}

func (s *TemporalCodeService) GetTemporalPasswordResettingCodeData(ctx context.Context, code string) (*model.TemporalPasswordResettingCodeData, error) {
	codeData, err := s.getTemporalCodeData(ctx, code, model.PasswordResettingCodeType)
	if err != nil {
		return nil, model.ErrTemporalCode.WithError(err).WithMessage("Failed to get temporal code data").Cause()
	}

	// unmarshal data
	data := model.TemporalPasswordResettingCodeData{}
	if err = json.Unmarshal(codeData, &data); err != nil {
		return nil, model.ErrTemporalCode.WithError(err).WithMessage("Failed to unmarshal data").Cause()
	}

	return &data, nil
}

func (s *TemporalCodeService) CreateTemporalEmailConfirmationCode(ctx context.Context, data model.TemporalEmailConfirmationCodeData) (string, error) {
	return s.createTemporalCode(ctx, model.EmailConfirmationCodeType, data)
}

func (s *TemporalCodeService) GetTemporalEmailConfirmationCodeData(ctx context.Context, code string) (*model.TemporalEmailConfirmationCodeData, error) {
	codeData, err := s.getTemporalCodeData(ctx, code, model.EmailConfirmationCodeType)
	if err != nil {
		return nil, model.ErrTemporalCode.WithError(err).WithMessage("Failed to get temporal code data").Cause()
	}

	// unmarshal data
	data := model.TemporalEmailConfirmationCodeData{}
	if err = json.Unmarshal(codeData, &data); err != nil {
		return nil, model.ErrTemporalCode.WithError(err).WithMessage("Failed to unmarshal data").Cause()
	}

	return &data, nil
}

func (s *TemporalCodeService) createTemporalCode(ctx context.Context, codeType int32, data interface{}) (string, error) {
	baseError := model.ErrTemporalCode.WithContext("codeType", codeType)

	id := uuid.Must(uuid.NewV7())

	jData, err := json.Marshal(data)
	if err != nil {
		return "", baseError.WithError(err).WithMessage("Failed to marshal data").Cause()
	}

	if err = s.repository.CreateTemporalCode(ctx, postgres.CreateTemporalCodeParams{
		ID:        id,
		ExpiredAt: time.Now().Add(s.ttl),
		CodeType:  codeType,
		Data:      jData,
	}); err != nil {
		return "", baseError.WithError(err).WithMessage("Failed to create temporal code").Cause()
	}
	return id.String(), nil
}

func (s *TemporalCodeService) getTemporalCodeData(ctx context.Context, code string, codeType int32) (json.RawMessage, error) {
	baseError := model.ErrTemporalCode.WithContext("codeType", codeType)
	baseInvalidTemporalCodeError := model.ErrTemporalCodeInvalidCode.WithContext("codeType", codeType)

	id, err := uuid.FromString(code)
	if err != nil {
		return nil, baseInvalidTemporalCodeError.WithMessage("Failed to parse temporal code id").Cause()
	}
	temporalCode, err := s.repository.GetTemporalCode(ctx, id)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, baseInvalidTemporalCodeError.Cause()
		}
		return nil, baseError.WithError(err).WithMessage("Failed to get temporal code").Cause()
	}

	// delete temporal code
	affected, err := s.repository.DeleteTemporalCode(ctx, id)
	if err != nil {
		return nil, baseError.WithError(err).WithMessage("Failed to delete temporal code").Cause()
	}

	if affected == 0 {
		return nil, model.ErrTemporalCodeNotFound.WithContext("codeType", codeType).Cause()
	}

	if temporalCode.CodeType != codeType || temporalCode.ExpiredAt.Before(time.Now()) {
		return nil, baseInvalidTemporalCodeError.Cause()
	}

	return temporalCode.Data, nil
}
