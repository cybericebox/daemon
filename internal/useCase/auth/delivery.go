package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/temporalCodeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/tools"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/gofrs/uuid"
)

type authDeliveryCommand struct {
	Kind     string
	Email    string
	Redirect string
}
type authDeliveryQueue struct {
	queue  worker.IEnqueuer
	cipher *secret.Cipher
	unit   postgres.IUnitOfWorker[IRepository]
}

// SetDeliveryQueue makes public signup and reset requests durable before acknowledging them.
func (u *AuthUseCase) SetDeliveryQueue(queue worker.IEnqueuer, cipher *secret.Cipher, unit postgres.IUnitOfWorker[IRepository]) {
	u.delivery = &authDeliveryQueue{queue: queue, cipher: cipher, unit: unit}
}
func authDeliveryKey(id uuid.UUID) string { return "auth-delivery:" + id.String() }

func (u *AuthUseCase) queueAuthDelivery(ctx context.Context, command authDeliveryCommand) error {
	d := u.delivery
	if d.cipher == nil || d.unit == nil {
		return model.ErrPlatform.WithMessage("Durable auth delivery is not configured").Err()
	}
	queue, ok := d.queue.(worker.TransactionalEnqueuer)
	if !ok {
		return model.ErrPlatform.WithMessage("Transactional auth queue is not configured").Err()
	}
	// Apply the public request quota once; a queued command's retries must never consume it again.
	kind := mailKindSignUp
	if command.Kind == "reset" {
		kind = mailKindReset
	}
	allowed, release := u.limits.mail.Reserve(kind, normalizeEmail(command.Email))
	if !allowed {
		return nil
	}
	committed := false
	defer func() {
		if !committed {
			release()
		}
	}()
	id := tools.NewUUIDv7()
	key := authDeliveryKey(id)
	raw, err := json.Marshal(command)
	if err != nil {
		return err
	}
	sealed, err := d.cipher.EncryptWithContext(raw, []byte(key))
	if err != nil {
		return err
	}
	data, err := json.Marshal(sealed)
	if err != nil {
		return err
	}
	txCtx, repo, unit, err := d.unit.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	tx, ok := postgres.TransactionFromContext(txCtx)
	if !ok {
		return model.ErrPlatform.WithMessage("Auth delivery transaction is missing").Err()
	}
	if err = temporalCodeRepo.New(repo).Create(txCtx, temporalCodeModel.NewCode(id, key, temporalCodeModel.AuthDeliveryCodeType, data, time.Now().Add(14*24*time.Hour))); err != nil {
		return err
	}
	if err = queue.EnqueueTx(txCtx, tx, jobsModel.NotifyArgs{AuthDeliveryID: &id}); err != nil {
		return err
	}
	if err = unit.Save(); err != nil {
		return err
	}
	committed = true
	return nil
}

// ProcessAuthDelivery resolves the account only after the durable request is accepted, then uses the standard notification queue.
func (u *AuthUseCase) ProcessAuthDelivery(ctx context.Context, id uuid.UUID) error {
	if u.delivery == nil || u.delivery.cipher == nil || u.delivery.unit == nil {
		return model.ErrPlatform.WithMessage("Durable auth delivery is not configured").Err()
	}

	txCtx, repo, unit, err := u.delivery.unit.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	ctx = txCtx
	// All account mutations, the normal notification handoff and consumption of this command share this commit.
	bound := &AuthUseCase{users: userRepo.New(repo), codes: temporalCodeRepo.New(repo), token: u.token, notifier: u.notifier, cfg: u.cfg, limits: u.limits, setupTokens: NewSetupTokenStore(repo, u.token)}
	key := authDeliveryKey(id)
	code, err := bound.codes.GetByCode(ctx, key)
	if repositoryTools.IsObjectNotFoundError(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var sealed string
	if err = json.Unmarshal(code.Data, &sealed); err != nil {
		return err
	}
	raw, err := u.delivery.cipher.DecryptWithContext(sealed, []byte(key))
	if err != nil {
		return err
	}
	var command authDeliveryCommand
	if err = json.Unmarshal(raw, &command); err != nil {
		return err
	}
	user, lookupErr := bound.users.GetByEmail(ctx, command.Email)
	ctx = context.WithValue(ctx, authDeliveryAcceptedKey{}, true)
	switch command.Kind {
	case "signup":
		if lookupErr != nil && !repositoryTools.IsObjectNotFoundError(lookupErr) {
			return lookupErr
		}
		err = bound.registerOrNotify(ctx, command.Email, command.Redirect, user, !repositoryTools.IsObjectNotFoundError(lookupErr))
	case "reset":
		if repositoryTools.IsObjectNotFoundError(lookupErr) {
			break
		}
		if lookupErr != nil {
			return lookupErr
		}
		err = bound.sendPasswordReset(ctx, user)
	default:
		return fmt.Errorf("unknown auth delivery kind %q", command.Kind)
	}
	if err != nil {
		return err
	}
	_, err = bound.codes.Delete(ctx, code.ID)
	if err != nil {
		return err
	}
	return unit.Save()
}

type authDeliveryAcceptedKey struct{}

func authDeliveryAccepted(ctx context.Context) bool {
	accepted, _ := ctx.Value(authDeliveryAcceptedKey{}).(bool)
	return accepted
}
