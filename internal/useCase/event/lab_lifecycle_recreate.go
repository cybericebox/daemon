package event

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/gofrs/uuid"
	"time"
)

func (u *EventUseCase) retainsLabReference(ctx context.Context, group, lab string) (bool, error) {
	return u.repo.IsOwnedRetainedLabReference(ctx, postgres.IsOwnedRetainedLabReferenceParams{LabGroupName: group, LabName: lab})
}

// Replacement never destroys the old generation to make a new one fit. Old
// definitions, objective pins, score and retained placement survive.
func (u *EventUseCase) recreateRetainedTeamStand(ctx context.Context, eventID, teamID, by uuid.UUID, now time.Time) (StandTeamView, error) {
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return StandTeamView{}, err
	}
	defer unit.Restore()
	if _, err = q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
		return StandTeamView{}, err
	}
	if err = eventLabRepo.New(q).LockAdmission(txCtx, teamID); err != nil {
		return StandTeamView{}, err
	}
	bindings, err := labBindingRepo.New(q).ListTeamLive(txCtx, teamID)
	if err != nil {
		return StandTeamView{}, err
	}
	seen := map[uuid.UUID]bool{}
	terminal := map[uuid.UUID]bool{}
	for _, b := range bindings {
		if !b.LabID.Valid || seen[b.LabID.UUID] {
			continue
		}
		seen[b.LabID.UUID] = true
		l, err := eventLabRepo.New(q).Lock(txCtx, b.LabID.UUID)
		if err != nil {
			return StandTeamView{}, err
		}
		if l.CloseReason == "solved" {
			terminal[l.ID] = true
			continue
		}
		expected := l.Revision
		if l.DesiredState == "Running" {
			if err = l.Close("manual", uuid.Must(uuid.NewV7()), now); err != nil {
				return StandTeamView{}, err
			}
			l.SetRetentionDeadline(now.Add(time.Duration(l.RetentionMinutes)*time.Minute), now)
			if _, err = eventLabRepo.New(q).Update(txCtx, l, expected); err != nil {
				return StandTeamView{}, err
			}
		}
		if err = eventLabRetentionRepo.New(q).Archive(txCtx, l.ID); err != nil {
			return StandTeamView{}, err
		}

	}
	for _, b := range bindings {
		if b.LabID.Valid && terminal[b.LabID.UUID] {
			continue
		}
		next := b.Generation + 1
		if _, err = labBindingRepo.New(q).Recreate(txCtx, b, labBindingModel.NextLabName(b.LabName, next), next); err != nil {
			return StandTeamView{}, err
		}
	}
	if err = unit.Save(); err != nil {
		return StandTeamView{}, err
	}
	u.wakeLabLifecycle(ctx)
	if err = u.RequestLabAccessSync(ctx, teamID); err != nil {
		return StandTeamView{}, err
	}
	current, err := u.standTeam(ctx, eventID, teamID)
	if err != nil {
		return StandTeamView{}, fmt.Errorf("read recreated stand: %w", err)
	}
	return toStandTeamView(current), nil
}
