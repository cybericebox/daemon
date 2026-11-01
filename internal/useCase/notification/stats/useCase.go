package stats

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

// MaxBulkResend is the most dispatches one resend request takes.
const MaxBulkResend = 200

// Resender queues the failed channels of one dispatch again (the dispatcher).
type Resender interface {
	ResendDispatch(ctx context.Context, dispatchID uuid.UUID) error
}

type Dependencies struct {
	Repo     dispatchRepo.Queries
	Resender Resender
}

type NotificationStatsUseCase struct {
	dispatches *dispatchRepo.Repository
	resender   Resender
}

func NewNotificationStatsUseCase(deps Dependencies) *NotificationStatsUseCase {
	return &NotificationStatsUseCase{
		dispatches: dispatchRepo.New(deps.Repo),
		resender:   deps.Resender,
	}
}

// ResendResult is the outcome of resending one dispatch; Err is nil when it was queued again.
type ResendResult struct {
	DispatchID uuid.UUID
	Err        error
}

// ResendDispatches sends the failed channels of each dispatch again. One result per distinct id in request
// order; a dispatch that cannot be resent never stops the others.
func (u *NotificationStatsUseCase) ResendDispatches(
	ctx context.Context,
	ids []uuid.UUID,
) []ResendResult {
	out := make([]ResendResult, 0, len(ids))
	seen := make(map[uuid.UUID]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		if u.resender == nil {
			out = append(
				out,
				ResendResult{
					DispatchID: id,
					Err:        model.ErrPlatform.WithMessage("Resending is not configured").Err(),
				},
			)
			continue
		}
		out = append(out, ResendResult{DispatchID: id, Err: u.resender.ResendDispatch(ctx, id)})
	}
	return out
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

// ErrUnknownFacetField is returned when the dispatch log has no facet for the asked field.
var ErrUnknownFacetField = errors.New("unknown facet field")

// DispatchFacets counts the dispatches per value of one field under the other filters of the log.
func (u *NotificationStatsUseCase) DispatchFacets(
	ctx context.Context,
	field string,
	f dispatchModel.ListDispatchesFilter,
) (map[string]int64, error) {
	if !slices.Contains(dispatchRepo.FacetFields(), field) {
		return nil, ErrUnknownFacetField
	}
	counts, err := u.dispatches.Facet(ctx, field, f)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to count the dispatches").
			Err()
	}
	return counts, nil
}
