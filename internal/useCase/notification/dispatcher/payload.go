package dispatcherUseCase

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/temporalCodeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/tools"
)

// payloadTTL outlives every River retry of a notify job (backoff stays well under it); the retention
// purge removes a payload nobody read.
const payloadTTL = 14 * 24 * time.Hour

// payloadStore keeps notification payloads as sealed temporal codes.
type payloadStore struct {
	codes  *temporalCodeRepo.Repository
	cipher *secret.Cipher
}

func payloadCode(dispatchID uuid.UUID) string { return "notify-payload:" + dispatchID.String() }

func payloadContext(dispatchID uuid.UUID) []byte {
	return []byte("notify-payload:" + dispatchID.String())
}

func (s *payloadStore) put(ctx context.Context, dispatchID, userID uuid.UUID, p dispatchModel.Payload) error {
	if s.cipher == nil {
		return model.ErrPlatform.WithMessage("PLATFORM_SECRETS_KEY is required to queue notifications").Err()
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	sealed, err := s.cipher.EncryptWithContext(raw, payloadContext(dispatchID))
	if err != nil {
		return err
	}
	data, err := json.Marshal(temporalCodeModel.TemporalNotificationPayloadCodeData{UserID: userID, Sealed: sealed})
	if err != nil {
		return err
	}
	return s.codes.Create(ctx, temporalCodeModel.NewCode(tools.NewUUIDv7(), payloadCode(dispatchID),
		temporalCodeModel.NotificationPayloadCodeType, data, time.Now().Add(payloadTTL)))
}

// get returns (payload, id of its row). A missing row is repositoryTools not-found.
func (s *payloadStore) get(ctx context.Context, dispatchID uuid.UUID) (dispatchModel.Payload, error) {
	if s.cipher == nil {
		return dispatchModel.Payload{}, model.ErrPlatform.WithMessage("PLATFORM_SECRETS_KEY is required to read queued notifications").Err()
	}
	code, err := s.codes.GetByCode(ctx, payloadCode(dispatchID))
	if err != nil {
		return dispatchModel.Payload{}, err
	}
	var data temporalCodeModel.TemporalNotificationPayloadCodeData
	if err = json.Unmarshal(code.Data, &data); err != nil {
		return dispatchModel.Payload{}, err
	}
	raw, err := s.cipher.DecryptWithContext(data.Sealed, payloadContext(dispatchID))
	if err != nil {
		return dispatchModel.Payload{}, err
	}
	var p dispatchModel.Payload
	return p, json.Unmarshal(raw, &p)
}

func (s *payloadStore) drop(ctx context.Context, dispatchID uuid.UUID) error {
	code, err := s.codes.GetByCode(ctx, payloadCode(dispatchID))
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil
		}
		return err
	}
	_, err = s.codes.Delete(ctx, code.ID)
	return err
}

// LoadNotificationPayload reads the sealed payload of a queued notification.
func (u *NotificationDispatcher) LoadNotificationPayload(ctx context.Context, dispatchID uuid.UUID) (dispatchModel.Payload, error) {
	p, err := u.payloads.get(ctx, dispatchID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return dispatchModel.Payload{}, dispatchModel.ErrPayloadGone
		}
		return dispatchModel.Payload{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read notification payload").Err()
	}
	return p, nil
}

// DiscardNotificationPayload removes the payload once its notification is done.
func (u *NotificationDispatcher) DiscardNotificationPayload(ctx context.Context, dispatchID uuid.UUID) error {
	if err := u.payloads.drop(ctx, dispatchID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to discard notification payload").Err()
	}
	return nil
}
