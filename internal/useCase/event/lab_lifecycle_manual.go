package event

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabAllocationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/requestIdempotencyRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"strconv"
	"time"
)

type ManualLabInput struct {
	ExpectedRevision int64
	IdempotencyKey   uuid.UUID
}

func (u *EventUseCase) StopOwnLab(ctx context.Context, eventID, userID, labID uuid.UUID, in ManualLabInput) (ParticipantLabView, error) {
	return u.manualLabCommand(ctx, eventID, userID, labID, in, false)
}
func (u *EventUseCase) RestartOwnLab(ctx context.Context, eventID, userID, labID uuid.UUID, in ManualLabInput) (ParticipantLabView, error) {
	return u.manualLabCommand(ctx, eventID, userID, labID, in, true)
}
func (u *EventUseCase) manualLabCommand(ctx context.Context, eventID, userID, labID uuid.UUID, in ManualLabInput, restart bool) (ParticipantLabView, error) {
	if in.IdempotencyKey == uuid.Nil || in.ExpectedRevision < 1 || labID == uuid.Nil {
		return ParticipantLabView{}, eventLabModel.ErrManualInput.Err()
	}
	if u.uow == nil {
		return ParticipantLabView{}, model.ErrPlatform.WithMessage("Laboratory transaction is not configured").Err()
	}
	now := time.Now().UTC()
	txCtx, q, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return ParticipantLabView{}, err
	}
	defer unit.Restore()
	if _, err = q.LockEventForLabSourceChange(txCtx, eventID); err != nil {
		return ParticipantLabView{}, err
	}
	if err = q.LockResourceCalendar(txCtx); err != nil {
		return ParticipantLabView{}, err
	}
	if _, err = q.LockEventConfigForLabSizing(txCtx, eventID); err != nil {
		return ParticipantLabView{}, err
	}
	p, err := participantRepo.New(q).Get(txCtx, eventID, userID)
	if err != nil {
		return ParticipantLabView{}, err
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return ParticipantLabView{}, participantModel.ErrParticipantAccessForbidden.Err()
	}
	teamID := *p.TeamID
	if err = eventLabRepo.New(q).LockAdmission(txCtx, teamID); err != nil {
		return ParticipantLabView{}, err
	}
	currentParticipant, err := participantRepo.New(q).Get(txCtx, eventID, userID)
	if err != nil {
		return ParticipantLabView{}, err
	}
	if currentParticipant.Status != participantModel.StatusApproved || currentParticipant.TeamID == nil || *currentParticipant.TeamID != teamID {
		return ParticipantLabView{}, eventLabModel.ErrManualOwnership.Err()
	}
	if err = requireTeamAdmitted(txCtx, eventTeamRepo.New(q), eventID, teamID); err != nil {
		return ParticipantLabView{}, err
	}
	if err = requireTeamFormed(txCtx, eventTeamRepo.New(q), eventID, teamID); err != nil {
		return ParticipantLabView{}, err
	}
	lab, err := eventLabRepo.New(q).Lock(txCtx, labID)
	if repositoryTools.IsObjectNotFoundError(err) || (err == nil && (lab.TeamID != teamID || lab.EventID != eventID)) {
		return ParticipantLabView{}, eventLabModel.ErrManualOwnership.Err()
	}
	if err != nil {
		return ParticipantLabView{}, err
	}
	hash := sha256.Sum256([]byte(eventID.String() + "\x00" + labID.String() + "\x00" + strconv.FormatInt(in.ExpectedRevision, 10) + "\x00" + strconv.FormatBool(restart)))
	idem := requestIdempotencyRepo.New(q)
	record, created, err := idem.Reserve(txCtx, requestIdempotencyRepo.Key{OwnerID: userID, Scope: "event-lab-manual", IdempotencyKey: in.IdempotencyKey}, hash[:], now, now.Add(24*time.Hour))
	if err != nil {
		return ParticipantLabView{}, err
	}
	if !created {
		if !bytes.Equal(record.RequestHash, hash[:]) {
			return ParticipantLabView{}, eventLabModel.ErrManualKeyConflict.Err()
		}
		if !record.Completed {
			return ParticipantLabView{}, eventLabModel.ErrManualKeyInProgress.Err()
		}
		return participantLabView(lab), nil
	}
	if lab.Revision != in.ExpectedRevision {
		return ParticipantLabView{}, eventLabModel.ErrManualRevision.Err()
	}
	config, err := eventConfigRepo.New(q).Get(txCtx, eventID)
	if err != nil {
		return ParticipantLabView{}, err
	}
	if config.TaskRevealMode != eventConfigModel.RevealAsReady {
		return ParticipantLabView{}, eventLabModel.ErrManualMode.Err()
	}
	if lab.CloseReason == "solved" {
		return ParticipantLabView{}, eventLabModel.ErrManualSolved.Err()
	}
	event, err := eventRepo.New(q).GetByID(txCtx, eventID)
	if err != nil {
		return ParticipantLabView{}, err
	}
	reachable, err := eventLabRevealRepo.New(q).Reachable(txCtx, labID, now)
	if err != nil {
		return ParticipantLabView{}, err
	}
	if !event.InfrastructureAllowed || !event.Lifecycle.RuntimeOpen(now) || !reachable {
		return ParticipantLabView{}, eventLabModel.ErrManualStage.Err()
	}
	caps, known := u.labCaps(txCtx, lab.Ref.Group)
	if !known || !caps.ConfirmedRuntime || (!restart && (!caps.PerLabStop || (lab.SnapshotMode == "required" && !caps.RequiredSnapshot))) || (restart && !caps.RetainedRestart) {
		return ParticipantLabView{}, eventLabModel.ErrManualUnsupported.Err()
	}
	stop, canRestart := lab.ManualCapabilities(true, true, now)
	expected := lab.Revision
	if restart {
		if !canRestart {
			return ParticipantLabView{}, eventLabModel.ErrManualRetained.Err()
		}
		if err = lab.Start(uuid.Must(uuid.NewV7()), now); err != nil {
			return ParticipantLabView{}, err
		}
		if !lab.Admit(lab.Allocation.ConfiguredRequests, lab.Allocation.SnapshotQuotaBytes, now) && !lab.AdmitKnown(lab.Allocation.ConfiguredRequests, lab.Allocation.SnapshotQuotaBytes, lab.DefinitionHash, lab.Generation, now) {
			return ParticipantLabView{}, eventLabModel.ErrManualState.Err()
		}
		if err = u.reserveLabCandidateInTransaction(txCtx, q, lab, config, now); err != nil {
			return ParticipantLabView{}, err
		}
		if err = u.requestGroupRunningInTransaction(txCtx, q, eventID, teamID, lab.Ref.Group, now); err != nil {
			return ParticipantLabView{}, err
		}
	} else {
		if !stop {
			return ParticipantLabView{}, eventLabModel.ErrManualState.Err()
		}
		if err = lab.Close("manual", uuid.Must(uuid.NewV7()), now); err != nil {
			return ParticipantLabView{}, err
		}
		until := now.Add(time.Duration(lab.RetentionMinutes) * time.Minute)
		lab.SetRetentionDeadline(until, now)
	}
	changed, err := eventLabRepo.New(q).Update(txCtx, lab, expected)
	if err != nil {
		return ParticipantLabView{}, err
	}
	if !changed {
		return ParticipantLabView{}, eventLabModel.ErrManualRevision.Err()
	}
	if err = u.requestLabAccessSyncInTransaction(txCtx, q, teamID, now); err != nil {
		return ParticipantLabView{}, err
	}
	view := participantLabView(lab)
	body, err := json.Marshal(view)
	if err != nil {
		return ParticipantLabView{}, err
	}
	completed, err := idem.Complete(txCtx, record, 200, body)
	if err != nil {
		return ParticipantLabView{}, err
	}
	if !completed {
		return ParticipantLabView{}, eventLabModel.ErrManualKeyInProgress.Err()
	}
	if err = unit.Save(); err != nil {
		return ParticipantLabView{}, err
	}
	u.wakeLabLifecycle(ctx)
	return view, nil
}
func (u *EventUseCase) reserveLabCandidateInTransaction(ctx context.Context, q IRepository, lab eventLabModel.Lab, cfg eventConfigModel.EventConfig, now time.Time) error {
	if err := q.LockResourceCalendar(ctx); err != nil {
		return err
	}
	r := eventLabAllocationRepo.New(q)
	rows, err := r.Labs(ctx, lab.EventID)
	if err != nil {
		return err
	}
	groups, err := r.Groups(ctx, lab.EventID)
	if err != nil {
		return err
	}
	held := eventLabModel.Compute{}
	var storage int64
	active := int32(0)
	groupFound := false
	for _, l := range rows {
		if l.ID == lab.ID {
			l = lab
		}
		c := l.HeldCompute()
		held.CPUMillicores += c.CPUMillicores
		held.MemoryBytes += c.MemoryBytes
		storage += l.Allocation.SnapshotQuotaBytes
		if l.TeamID == lab.TeamID && l.ID != lab.ID && l.HoldsRuntime() {
			active++
		}
	}
	if cfg.EffectiveLabPolicy().MaxActiveLabsPerTeam != nil && active >= *cfg.EffectiveLabPolicy().MaxActiveLabsPerTeam {
		return eventLabModel.ErrManualLimit.Err()
	}
	for _, g := range groups {
		c := g.Lifecycle.HeldCompute()
		if g.TeamID == lab.TeamID {
			total := g.Sizes.Total()
			c.CPUMillicores = max(c.CPUMillicores, total.CPUMillicores)
			c.MemoryBytes = max(c.MemoryBytes, total.MemoryBytes)
		}
		held.CPUMillicores += c.CPUMillicores
		held.MemoryBytes += c.MemoryBytes
		if g.TeamID == lab.TeamID {
			groupFound = true
		}
	}
	budget, err := r.Budget(ctx, lab.EventID)
	if err != nil {
		return err
	}
	if !groupFound || budget.Unplaced > 0 || len(budget.Placement) == 0 || !budget.Window.Contains(now) || held.CPUMillicores > budget.Size.CPUMillicores || held.MemoryBytes > budget.Size.MemoryBytes || storage > budget.SizeSnapshotQuotaBytes {
		return eventLabModel.ErrManualLimit.Err()
	}
	return nil
}
func (u *EventUseCase) allocationBudget(ctx context.Context, id uuid.UUID) (calModel.Reservation, error) {
	return eventLabAllocationRepo.New(u.repo).Budget(ctx, id)
}
