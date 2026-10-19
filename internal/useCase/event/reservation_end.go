package event

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/model"
)

// groupLocator says which agent holds a lab group (the fleet).
type groupLocator interface {
	GroupAgent(ctx context.Context, group string) (uuid.UUID, bool, error)
}

// groupsOnAgent lists the event's team groups that live on the agent.
func (u *EventUseCase) groupsOnAgent(ctx context.Context, eventID, agentID uuid.UUID) ([]string, error) {
	locator, ok := u.infra.(groupLocator)
	if !ok {
		return nil, nil
	}
	groups, err := u.stands.ListLabGroups(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event laboratory groups").Err()
	}
	var out []string
	for _, group := range groups {
		agent, found, gErr := locator.GroupAgent(ctx, group)
		if gErr != nil {
			return nil, gErr
		}
		if found && agent == agentID {
			out = append(out, group)
		}
	}
	return out, nil
}

// ReservationPartEnding is the ending notice of the event's reservation part on an agent after the event: at the end
// the cluster deletes everything under it, snapshots included, so the labs that still run are stopped now (with the
// final snapshot their policy asks for) while the cluster still has them.
func (u *EventUseCase) ReservationPartEnding(ctx context.Context, eventID, agentID uuid.UUID) error {
	if !u.lifecycleControls || u.uow == nil {
		return nil
	}
	groups, err := u.groupsOnAgent(ctx, eventID, agentID)
	if err != nil || len(groups) == 0 {
		return err
	}
	running, err := eventLabRepo.New(u.repo).RunningInGroups(ctx, eventID, groups)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list the running event labs").Err()
	}
	now := time.Now().UTC()
	var errs []error
	for _, id := range running {
		txCtx, q, unit, e := u.uow.UnitOfWork(ctx)
		if e != nil {
			errs = append(errs, e)
			continue
		}
		func() {
			defer unit.Restore()
			l, e := eventLabRepo.New(q).Lock(txCtx, id)
			if e != nil || l.DesiredState != "Running" {
				return
			}
			expected := l.Revision
			if e = l.Close("event", uuid.Must(uuid.NewV7()), now); e != nil {
				errs = append(errs, e)
				return
			}
			if _, e = eventLabRepo.New(q).Update(txCtx, l, expected); e != nil {
				errs = append(errs, e)
				return
			}
			if e = unit.Save(); e != nil {
				errs = append(errs, e)
			}
		}()
	}
	u.wakeLabLifecycle(ctx)
	return errors.Join(errs...)
}

// ReservationPartEnded releases the event's labs on an agent whose reservation part ended: the cluster deleted them.
// Their groups are forgotten (placement released) and their bindings recorded as removed. It is the expected end of
// the reservation, not a failure: no stand failure, no failed-lab signal.
func (u *EventUseCase) ReservationPartEnded(ctx context.Context, eventID, agentID uuid.UUID) error {
	groups, err := u.groupsOnAgent(ctx, eventID, agentID)
	if err != nil {
		return err
	}
	var errs []error
	for _, group := range groups {
		if dErr := u.infra.DestroyLabGroup(ctx, group); dErr != nil {
			log.Warn().Err(dErr).Str("lab_group", group).Msg("Failed to release a lab group whose reservation ended")
			errs = append(errs, dErr)
			continue
		}
		if mErr := u.labBindings.MarkGroupDestroyed(ctx, eventID, group); mErr != nil {
			errs = append(errs, model.ErrPlatform.WithError(mErr).WithMessage("Failed to record the labs of an ended reservation").Err())
		}
	}
	return errors.Join(errs...)
}
