package event

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	exercise "github.com/cybericebox/daemon/internal/model/eventExercise"
	lab "github.com/cybericebox/daemon/internal/model/eventLab"
	binding "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/gofrs/uuid"
	"sort"
	"time"
)

type initialAbsence interface {
	InitialDeploymentAbsent(context.Context, lab.Ref) (bool, error)
}

// retryInitialFailedTeam performs no Lab mutation. It owns source/team/Lab locks
// while current producer absence and exact conditional binding ownership are checked.
func (u *EventUseCase) retryInitialFailedTeam(ctx context.Context, eventID, teamID uuid.UUID) (bool, error) {
	tx, q, unit, e := u.uow.UnitOfWork(ctx)
	if e != nil {
		return false, e
	}
	defer unit.Restore()
	if _, e = q.LockEventForLabSourceChange(tx, eventID); e != nil {
		return false, e
	}
	if e = eventLabRepo.New(q).LockAdmission(tx, teamID); e != nil {
		return false, e
	}
	repo := labBindingRepo.New(q)
	bindings, e := repo.ListTeamLive(tx, teamID)
	if e != nil {
		return false, e
	}
	if len(bindings) == 0 {
		return false, nil
	}
	ids := map[uuid.UUID]bool{}
	failed := false
	for _, b := range bindings {
		if b.EventID != eventID || b.EventTeamID != teamID || !b.LabID.Valid {
			return true, fmt.Errorf("initial retry binding ownership changed")
		}
		if b.DeployedAt != nil || (b.Readiness != binding.ReadinessFailed && b.Readiness != binding.ReadinessPending) {
			return false, nil
		}
		failed = failed || b.Readiness == binding.ReadinessFailed
		ids[b.LabID.UUID] = true
	}
	ordered := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	labs := []lab.Lab{}
	for _, id := range ordered {
		l, e := eventLabRepo.New(q).Lock(tx, id)
		if e != nil {
			return false, e
		}
		if l.EventID != eventID || l.TeamID != teamID {
			return true, fmt.Errorf("initial retry canonical ownership changed")
		}
		if !l.CanRetryInitialDeployment() {
			if failed {
				return true, fmt.Errorf("failed initial deployment identity changed")
			}
			return false, nil
		}
		for _, b := range bindings {
			if b.LabID.UUID == l.ID && (b.LabGroupName != l.Ref.Group || b.LabName != l.Ref.Lab || b.Generation != l.Generation) {
				return true, fmt.Errorf("initial retry binding identity changed")
			}
		}
		attachment, err := eventExerciseRepo.New(q).GetByID(tx, eventID, l.EventExerciseID)
		if err != nil {
			return true, err
		}
		if attachment.Status != exercise.StatusActive || attachment.SupersededAt != nil || attachment.ExerciseVersionID != l.DefinitionVersionID {
			return true, fmt.Errorf("initial retry pinned definition changed")
		}
		labs = append(labs, l)
	}
	// A concurrent duplicate after the first retry is idempotent while still uncreated.
	port, ok := u.infra.(initialAbsence)
	if !ok {
		return true, fmt.Errorf("qualified initial deployment absence unavailable")
	}
	for _, l := range labs {
		probe, cancel := context.WithTimeout(tx, 30*time.Second)
		absent, e := port.InitialDeploymentAbsent(probe, l.Ref)
		cancel()
		if e != nil {
			return true, e
		}
		if !absent {
			return true, fmt.Errorf("initial deployment already has native birth")
		}
	}
	if !failed {
		return true, nil
	}
	for _, l := range labs {
		expected := int64(0)
		for _, b := range bindings {
			if b.LabID.UUID == l.ID && b.Readiness == binding.ReadinessFailed {
				expected++
			}
		}
		if expected == 0 {
			continue
		}
		n, e := repo.RetryInitial(tx, l)
		if e != nil {
			return true, e
		}
		if n != expected {
			return true, fmt.Errorf("initial failed binding ownership changed")
		}
	}
	if e = unit.Save(); e != nil {
		return true, e
	}
	return true, nil
}

func (u *EventUseCase) standTeamViewAfterInitialRetry(ctx context.Context, eventID, teamID uuid.UUID) (StandTeamView, error) {
	current, e := u.standTeam(ctx, eventID, teamID)
	if e != nil {
		return StandTeamView{}, e
	}
	return toStandTeamView(current), nil
}
