package stats

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

type Dependencies struct {
	Repo dispatchRepo.Queries
}

type NotificationStatsUseCase struct {
	dispatches *dispatchRepo.Repository
}

func NewNotificationStatsUseCase(deps Dependencies) *NotificationStatsUseCase {
	return &NotificationStatsUseCase{dispatches: dispatchRepo.New(deps.Repo)}
}

func (u *NotificationStatsUseCase) ListDispatches(ctx context.Context, f dispatchModel.ListDispatchesFilter) ([]dispatchModel.DispatchDetail, int64, error) {
	dispatches, total, err := u.dispatches.List(ctx, f)
	if err != nil {
		return nil, 0, model.ErrPlatform.WithError(err).WithMessage("Failed to list dispatches").Err()
	}
	return dispatches, total, nil
}

func (u *NotificationStatsUseCase) GetDispatch(ctx context.Context, id uuid.UUID) (*dispatchModel.DispatchDetail, error) {
	info, err := u.dispatches.Get(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, notificationModel.ErrDispatchNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get dispatch").Err()
	}
	targets, err := u.dispatches.ListTargets(ctx, id)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get dispatch targets").Err()
	}
	return &dispatchModel.DispatchDetail{DispatchInfo: info, Targets: targets}, nil
}

func (u *NotificationStatsUseCase) GetStats(ctx context.Context, since time.Time) (*dispatchModel.Stats, error) {
	byStatus, err := u.dispatches.CountByStatusSince(ctx, since)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to count dispatches by status").Err()
	}
	byType, err := u.dispatches.CountByTypeSince(ctx, since)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to count dispatches by type").Err()
	}
	byChannel, err := u.dispatches.CountTargetsByChannelStatusSince(ctx, since)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to count targets by channel").Err()
	}
	s := &dispatchModel.Stats{
		Since:     since,
		ByStatus:  byStatus,
		ByType:    byType,
		ByChannel: byChannel,
	}
	for _, r := range byStatus {
		s.Total += r.Count
	}
	return s, nil
}
