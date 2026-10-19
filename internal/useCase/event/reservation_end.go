package event

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabGroupRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
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
func (u *EventUseCase) ReservationPartEnding(ctx context.Context, eventID, agentID uuid.UUID, now time.Time) error {
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
func (u *EventUseCase) ReservationPartEnded(ctx context.Context, eventID, agentID uuid.UUID, now time.Time) error {
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
		if eErr := u.endLifecyclesWithReservation(ctx, group, now); eErr != nil {
			errs = append(errs, eErr)
		}
		u.releaseTeamUnit(ctx, group)
	}
	return errors.Join(errs...)
}

// endLifecyclesWithReservation settles the lifecycle of a team group the cluster deleted with its reservation, and of
// every lab in it: the lifecycle stops chasing them (no start, no stop, no failure) and they hold nothing.
func (u *EventUseCase) endLifecyclesWithReservation(ctx context.Context, group string, now time.Time) error {
	if !u.lifecycleControls || u.uow == nil {
		return nil
	}
	_, teamID, ok := labBindingModel.ParseGroupName(group)
	if !ok {
		return nil
	}
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	labs := eventLabRepo.New(q)
	if err = labs.LockAdmission(txCtx, teamID); err != nil && !repositoryTools.IsObjectNotFoundError(err) {
		return err
	}
	groups := eventLabGroupRepo.New(q)
	children, err := groups.LockChildren(txCtx, teamID)
	if err != nil {
		return err
	}
	for _, l := range children {
		if l.Ref.Group != group {
			continue
		}
		expected := l.Revision
		if !l.EndWithReservation(uuid.Must(uuid.NewV7()), now) {
			continue
		}
		if _, err = labs.Update(txCtx, l, expected); err != nil {
			return err
		}
	}
	g, err := groups.Lock(txCtx, teamID)
	switch {
	case repositoryTools.IsObjectNotFoundError(err):
	case err != nil:
		return err
	case g.Name == group:
		expected := g.Revision
		if g.EndWithReservation(uuid.Must(uuid.NewV7()), now) {
			if _, err = groups.Update(txCtx, g, expected); err != nil {
				return err
			}
		}
	}
	return unit.Save()
}

// teamUnitReleaser forgets the agent of a team's unit of the event reservation (the calendar).
type teamUnitReleaser interface {
	ReleaseTeamUnit(ctx context.Context, eventID, teamID uuid.UUID) error
}

// releaseTeamUnit forgets the agent of the team of a destroyed group, so a new group of the team is placed again
// from the current shares. A failure is logged: the unit stays on its agent.
func (u *EventUseCase) releaseTeamUnit(ctx context.Context, group string) {
	releaser, ok := u.resourceGate.(teamUnitReleaser)
	if !ok {
		return
	}
	eventID, teamID, ok := labBindingModel.ParseGroupName(group)
	if !ok {
		return
	}
	if err := releaser.ReleaseTeamUnit(ctx, eventID, teamID); err != nil {
		log.Warn().Err(err).Str("lab_group", group).Msg("Failed to release the reservation unit of a destroyed group")
	}
}
