package event

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/model"
)

// CaptureDueScoringPopulations is invoked by the periodic event-runtime worker.
// It serializes with team changes, records a stable approved-unit count once,
// and leaves empty events uncaptured because no dynamic score can be computed.
func (u *EventUseCase) CaptureDueScoringPopulations(ctx context.Context) error {
	due, err := u.events.ListDueForScoringPopulation(ctx, time.Now())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list due scoring populations").Err()
	}
	for _, item := range due {
		if u.uow == nil {
			return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
		}
		txCtx, txRepo, unit, txErr := u.uow.UnitOfWork(ctx)
		if txErr != nil {
			return txErr
		}
		if txErr = eventRepo.New(txRepo).LockForTeamChange(txCtx, item.EventID); txErr != nil {
			_ = unit.Restore()
			return model.ErrPlatform.WithError(txErr).WithMessage("Failed to lock event scoring population").Err()
		}
		// A team is the competitive unit in both participation modes. In an
		// individual event every approved participant has a personal team.
		count, txErr := eventTeamRepo.New(txRepo).CountApproved(txCtx, item.EventID)
		if txErr == nil && count > 0 {
			_, txErr = eventRepo.New(txRepo).CaptureScoringPopulation(txCtx, item.EventID, int32(count), time.Now())
		}
		if txErr != nil {
			_ = unit.Restore()
			return model.ErrPlatform.WithError(txErr).WithMessage("Failed to capture scoring population").Err()
		}
		if err = unit.Save(); err != nil {
			_ = unit.Restore()
			return model.ErrPlatform.WithError(err).WithMessage("Failed to save scoring population").Err()
		}
		_ = unit.Restore()
	}
	return nil
}
