package event

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/gofrs/uuid"
)

type ChallengeRuntimeView struct {
	Status exerciseModel.LabDeployStatus
	Lab    *ParticipantLabView
	// State is the stand as the participant should read it (see StandState*); Status.Access holds addresses
	// only in the ready state.
	State string
}

// The stable codes of ChallengeRuntimeView.State.
const (
	StandStateReady       = "ready"       // the addresses are current
	StandStateStarting    = "starting"    // the stand is being prepared for the first time
	StandStateRestarting  = "restarting"  // it ran and is coming back (a device or the whole lab restarts or is recreated)
	StandStateUnavailable = "unavailable" // the platform cannot reach the laboratory right now; try again
	StandStateFailed      = "failed"      // the stand failed; the organizers were told and it is retried
	StandStateClosed      = "closed"      // the lab is closed
)

// standRuntimeState reads the stand's state from the binding, the canonical lab and the agent's last answer. lab and
// binding may be nil; ready is the final verdict that the addresses are current.
func standRuntimeState(binding *labBindingModel.Binding, lab *eventLabModel.Lab, ready, agentUnreachable bool) string {
	if lab != nil && (lab.ClosedAt != nil || lab.DesiredState != "Running") {
		return StandStateClosed
	}
	if ready {
		return StandStateReady
	}
	if binding != nil && (binding.Readiness == labBindingModel.ReadinessFailed ||
		(binding.Readiness == labBindingModel.ReadinessPending && binding.RetryAttempts > 0 && binding.DeployedAt == nil && binding.FailureReason != "")) {
		return StandStateFailed
	}
	if agentUnreachable {
		return StandStateUnavailable
	}
	// A lab the agent once gave an identity (or a recreated generation) that is not ready now is coming back.
	if (lab != nil && lab.AgentUID != "") || (binding != nil && binding.Generation > 0) {
		return StandStateRestarting
	}
	return StandStateStarting
}

// Ownership and admission precede every lifecycle read, including settled closure.
func (u *EventUseCase) GetOwnLabLifecycle(ctx context.Context, eventID, userID, labID uuid.UUID) (ParticipantLabView, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ParticipantLabView{}, participantModel.ErrParticipantAccessForbidden.WithError(err).Err()
		}
		return ParticipantLabView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return ParticipantLabView{}, participantModel.ErrParticipantAccessForbidden.Err()
	}
	if err = requireTeamAdmitted(ctx, u.teams, eventID, *p.TeamID); err != nil {
		return ParticipantLabView{}, err
	}
	if err = requireTeamFormed(ctx, u.teams, eventID, *p.TeamID); err != nil {
		return ParticipantLabView{}, err
	}
	lab, err := u.labs.Get(ctx, labID)
	if repositoryTools.IsObjectNotFoundError(err) || (err == nil && (lab.TeamID != *p.TeamID || lab.EventID != eventID)) {
		return ParticipantLabView{}, eventLabModel.ErrNotFound.Err()
	}
	if err != nil {
		return ParticipantLabView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read laboratory lifecycle").Err()
	}
	return u.participantLabCapabilities(ctx, lab)
}

func (u *EventUseCase) fillBoardLabs(ctx context.Context, rows []teamChallengeRepo.PublishedChallenge, views []OwnChallengeView) error {
	byQuestion := map[uuid.UUID]uuid.UUID{}
	for _, row := range rows {
		if row.LabID.Valid {
			byQuestion[row.Challenge.EventChallengeID] = row.LabID.UUID
		}
	}
	cache := map[uuid.UUID]*ParticipantLabView{}
	for i := range views {
		id, ok := byQuestion[views[i].EventChallengeID]
		if !ok {
			continue
		}
		view := cache[id]
		if view == nil {
			lab, err := u.labs.Get(ctx, id)
			if err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to read board laboratory lifecycle").Err()
			}
			safe, viewErr := u.participantLabCapabilities(ctx, lab)
			if viewErr != nil {
				return viewErr
			}
			view = &safe
			cache[id] = view
		}
		views[i].Lab = view
	}
	return nil
}

func requireLabOpen(lab eventLabModel.Lab) error {
	if lab.ClosedAt != nil || lab.DesiredState != "Running" {
		return eventLabModel.ErrAccessClosed.Err()
	}
	return nil
}

func (u *EventUseCase) GetOwnChallengeRuntime(ctx context.Context, eventID, userID, challengeID uuid.UUID) (ChallengeRuntimeView, error) {
	// Status uses all existing publication/prerequisite/stage gates.
	p, err := u.requireOwnAvailableChallenge(ctx, eventID, userID, challengeID)
	if err != nil {
		return ChallengeRuntimeView{}, err
	}
	lab, err := u.labs.GetForChallenge(ctx, *p.TeamID, challengeID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			binding, bindingErr := u.labBindings.Get(ctx, *p.TeamID, challengeID)
			if bindingErr == nil && !binding.LabID.Valid {
				if _, err = u.requireEventLaboratories(ctx, eventID); err != nil {
					return ChallengeRuntimeView{}, err
				}
				if u.infra == nil {
					return ChallengeRuntimeView{}, infraUnavailable()
				}
				status, statusErr := u.infra.LabStatus(ctx, binding.LabGroupName, binding.LabName)
				if statusErr != nil {
					return ChallengeRuntimeView{State: standRuntimeState(&binding, nil, false, true)}, nil
				}
				// A binding without a canonical lab (a legacy stand) is read from the agent alone.
				return ChallengeRuntimeView{Status: status, State: standRuntimeState(&binding, nil, status.Ready, false)}, nil
			}
			return ChallengeRuntimeView{}, eventStandModel.ErrStandLabNotFound.Err()
		}
		return ChallengeRuntimeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read runtime laboratory lifecycle").Err()
	}
	safe, err := u.participantLabCapabilities(ctx, lab)
	if err != nil {
		return ChallengeRuntimeView{}, err
	}
	out := ChallengeRuntimeView{Lab: &safe, Status: exerciseModel.LabDeployStatus{Access: []exerciseModel.LabAccess{}}}
	if safe.LogicalClosed || lab.DesiredState != "Running" {
		out.Status.Phase = "Closed"
		out.State = StandStateClosed
		return out, nil
	}
	if _, err = u.requireEventLaboratories(ctx, eventID); err != nil {
		return ChallengeRuntimeView{}, err
	}
	if u.infra == nil {
		return ChallengeRuntimeView{}, infraUnavailable()
	}
	binding, bindingErr := u.labBindings.Get(ctx, *p.TeamID, challengeID)
	var bindingRef *labBindingModel.Binding
	if bindingErr == nil {
		bindingRef = &binding
	}
	status, err := u.infra.LabStatus(ctx, lab.Ref.Group, lab.Ref.Lab)
	if err != nil {
		if ctx.Err() != nil {
			return ChallengeRuntimeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get lab status").Err()
		}
		// The participant is told the stand is temporarily unreachable instead of getting a failure page.
		log.Warn().Err(err).Str("lab_id", lab.ID.String()).Msg("Lab status unavailable for a participant")
		out.Status.Phase = safe.RuntimeState
		out.State = standRuntimeState(bindingRef, &lab, false, true)
		return out, nil
	}
	current, err := u.labs.GetForChallenge(ctx, *p.TeamID, challengeID)
	if err != nil {
		return ChallengeRuntimeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to recheck runtime laboratory lifecycle").Err()
	}
	// The agent says the Lab is ready but the canonical row never recorded it (khctf: addresses stayed «—» for a
	// ready Lab): record the plain readiness now, as the stand engine does, instead of waiting for a reload.
	if !current.ReadyForAccess() && status.Ready {
		if healed, healErr := u.adoptPlainReadiness(ctx, current, status); healErr != nil {
			log.Warn().Err(healErr).Str("lab_id", current.ID.String()).Msg("Failed to refresh the runtime of a ready lab")
		} else if healed {
			if current, err = u.labs.GetForChallenge(ctx, *p.TeamID, challengeID); err != nil {
				return ChallengeRuntimeView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to recheck runtime laboratory lifecycle").Err()
			}
		}
	}
	safe, err = u.participantLabCapabilities(ctx, current)
	if err != nil {
		return ChallengeRuntimeView{}, err
	}
	out.Lab = &safe
	// Never hand out stale addresses when closure/start changed during the live read.
	if current.ClosedAt != nil || current.ID != lab.ID || current.Revision != lab.Revision || current.DesiredState != "Running" {
		out.Status.Phase = safe.RuntimeState
		out.State = standRuntimeState(bindingRef, &current, false, false)
		if current.ClosedAt == nil && current.ID == lab.ID && current.DesiredState == "Running" {
			out.State = StandStateRestarting
		}
		return out, nil
	}
	out.Status = status
	if !current.ReadyForAccess() || !status.Ready || status.LabUID != current.AgentUID || status.LabGeneration < current.AgentGeneration {
		out.Status.Ready = false
		out.Status.Phase = safe.RuntimeState
		out.Status.Access = []exerciseModel.LabAccess{}
		out.Status.Gateways = nil
	}
	out.State = standRuntimeState(bindingRef, &current, out.Status.Ready, false)
	return out, nil
}

// adoptPlainReadiness records, for a Lab certified by its plain readiness (no managed lifecycle: the agent reports no
// confirmed runtime, or the controls are off), the identity and readiness the agent reports now. It touches only the
// original Running revision with a matching identity, like the stand engine, and reports whether the row changed.
func (u *EventUseCase) adoptPlainReadiness(ctx context.Context, lab eventLabModel.Lab, status exerciseModel.LabDeployStatus) (bool, error) {
	managed := u.lifecycleControls && !u.runtimeUnconfirmed(ctx, lab.Ref.Group)
	if managed || lab.Revision != 1 || lab.DesiredState != "Running" || lab.ClosedAt != nil || status.LabUID == "" || status.LabGeneration <= 0 {
		return false, nil
	}
	now := time.Now().UTC()
	if lab.AgentUID == "" {
		adopted, err := u.labs.RecordInitialIdentity(ctx, lab.ID, lab.Ref, status.LabUID, status.LabGeneration, now)
		if err != nil || !adopted {
			return false, err
		}
	} else if lab.AgentUID != status.LabUID || status.LabGeneration < lab.AgentGeneration {
		return false, nil
	}
	return u.labs.RecordInitialReadiness(ctx, lab.ID, lab.Ref, status.LabUID, status.LabGeneration, true, now)
}
