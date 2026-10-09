package event

import (
	"context"
	"fmt"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"sync"
	"time"

	"github.com/cybericebox/laboratory/pkg/vpnprobe"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
)

// GetOwnChallengeLabStatus exposes a Lab only after the exact team challenge
// is available. A board-configured challenge that is still preparing has no
// participant runtime surface.
func (u *EventUseCase) GetOwnChallengeLabStatus(ctx context.Context, eventID, userID, challengeID uuid.UUID) (exerciseModel.LabDeployStatus, error) {
	out, err := u.GetOwnChallengeRuntime(ctx, eventID, userID, challengeID)
	return out.Status, err
}

// requireRuntimeOpenAt makes request-time boundary checks explicit for
// operations that carry a transport-captured arrival instant. It must never
// substitute a later database-processing time for that instant.
func requireRuntimeOpenAt(ctx context.Context, repo *eventRepo.Repository, eventID uuid.UUID, at time.Time) error {
	event, err := repo.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event lifecycle").Err()
	}
	if !event.Lifecycle.RuntimeOpen(at) {
		return eventModel.ErrEventRuntimeNotOpen.Err()
	}
	return nil
}

func (u *EventUseCase) requireOwnAvailableChallenge(ctx context.Context, eventID, userID, challengeID uuid.UUID) (participantModel.Participant, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		return participantModel.Participant{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return participantModel.Participant{}, participantModel.ErrParticipantNotApproved.Err()
	}
	if err = requireTeamAdmitted(ctx, u.teams, eventID, *p.TeamID); err != nil {
		return participantModel.Participant{}, err
	}
	if err = requireTeamFormed(ctx, u.teams, eventID, *p.TeamID); err != nil {
		return participantModel.Participant{}, err
	}
	tc, err := u.teamChallenges.Get(ctx, *p.TeamID, challengeID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return participantModel.Participant{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
		}
		return participantModel.Participant{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get team challenge").Err()
	}
	if tc.EventID != eventID || tc.Readiness != teamChallengeModel.ReadinessPublished {
		return participantModel.Participant{}, teamChallengeModel.ErrTeamChallengeTransition.Err()
	}
	// The same locks as the board, submit and files: a task the organizers
	// unpublished, whose set was removed from the event, or whose prerequisites
	// the team has not solved has no lab surface either.
	challenge, err := u.eventChallenges.GetForEvent(ctx, eventID, tc.EventChallengeID)
	if err != nil {
		return participantModel.Participant{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenge").Err()
	}
	if !challenge.Published {
		return participantModel.Participant{}, teamChallengeModel.ErrTeamChallengeTransition.Err()
	}
	set, err := u.eventExercises.GetByID(ctx, eventID, challenge.EventExerciseID)
	if err != nil {
		return participantModel.Participant{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if set.Status == eventExerciseModel.StatusDetached {
		return participantModel.Participant{}, teamChallengeModel.ErrTeamChallengeTransition.Err()
	}
	// The stage gate of the lab link and status: an upcoming stage hides the task, a closed one has no lab access.
	if set.StageID != nil {
		stage, stageErr := u.stages.Get(ctx, eventID, *set.StageID)
		if stageErr != nil {
			return participantModel.Participant{}, model.ErrPlatform.WithError(stageErr).WithMessage("Failed to get event stage").Err()
		}
		switch eventModel.StagePhaseAt(&stage, time.Now()) {
		case eventModel.StagePhaseUpcoming:
			return participantModel.Participant{}, teamChallengeModel.ErrTeamChallengeTransition.Err()
		case eventModel.StagePhaseClosed:
			return participantModel.Participant{}, eventChallengeModel.ErrEventChallengeStageClosed.Err()
		}
	}
	if err = requirePrerequisitesSolved(ctx, u.eventChallenges, u.teamChallenges, *p.TeamID, tc.EventChallengeID); err != nil {
		return participantModel.Participant{}, err
	}
	return p, nil
}

// ensureParticipantVPNConfig uses the group scheduled when the team was
// created and never asks the agent to recreate an existing client. The agent
// returns a complete WireGuard config only at creation time;
// later reads intentionally redact the private key, so the encrypted control
// plane copy is the authoritative reusable credential.
//
// Calls for one person are serialized and re-read the store under the lock, so concurrent syncs
// create one client; an agent client without a stored config is recreated by the agent adapter.
func ensureParticipantVPNConfig(ctx context.Context, store VPNStore, infra Infrastructure, group string, eventID, userID uuid.UUID) (string, error) {
	lock := participantVPNLocks.lock(eventID.String() + "/" + userID.String())
	defer lock()
	scopeRef := uuid.NullUUID{UUID: eventID, Valid: true}
	existing, err := store.GetConfig(ctx, userID, vpnModel.ScopeEvent, scopeRef)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to read participant VPN config").Err()
	}
	if existing != "" {
		return existing, nil
	}
	if infra == nil {
		return "", infraUnavailable()
	}
	config, err := infra.EnsureLabClient(ctx, group, participantLabClientName(userID))
	if err != nil {
		if _, terminating := infraModel.AsTerminating(err); terminating {
			return "", infraModel.ErrLabAccessRetry.WithError(err).Err()
		}
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to provision participant VPN access").Err()
	}
	if config == "" {
		return "", model.ErrPlatform.WithMessage("Laboratory agent issued an empty VPN config").Err()
	}
	if err = store.StoreConfig(ctx, userID, vpnModel.ScopeEvent, scopeRef, config); err != nil {
		return "", err
	}
	return config, nil
}

// GetOwnLabVPNConfig issues or returns the caller's event-scoped credential.
// Approval and team membership are checked before the config is read; neither
// an exercise nor an open runtime is needed for the tunnel-only test gateway.
func (u *EventUseCase) GetOwnLabVPNConfig(ctx context.Context, eventID, userID uuid.UUID) (string, error) {
	if u.vpn == nil {
		return "", model.ErrPlatform.WithMessage("VPN storage is not configured").Err()
	}
	if _, err := u.requireOwnVPNGroup(ctx, eventID, userID); err != nil {
		return "", err
	}
	// The client is created when the person joins a team (the access sync), never
	// here: this only hands out the stored config.
	config, err := u.vpn.GetConfig(ctx, userID, vpnModel.ScopeEvent, uuid.NullUUID{UUID: eventID, Valid: true})
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to read participant VPN config").Err()
	}
	if config == "" {
		return "", eventStandModel.ErrStandLabClientMissing.Err()
	}
	return config, nil
}

type VPNProbeStatusView struct {
	GatewayIP string
	ProbeURL  string
}

type vpnSubnetReader interface {
	GetVPNClientSubnet(context.Context, string) (string, error)
}

// GetOwnVPNProbeStatus exposes only the assigned tunnel gateway to an approved
// team member while this event's VPN setting is enabled.
func (u *EventUseCase) GetOwnVPNProbeStatus(ctx context.Context, eventID, userID uuid.UUID) (VPNProbeStatusView, error) {
	group, err := u.requireOwnVPNGroup(ctx, eventID, userID)
	if err != nil {
		return VPNProbeStatusView{}, err
	}
	reader, ok := u.infra.(vpnSubnetReader)
	if !ok {
		return VPNProbeStatusView{}, infraUnavailable()
	}
	subnet, err := reader.GetVPNClientSubnet(ctx, group)
	if err != nil {
		return VPNProbeStatusView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read team VPN gateway").Err()
	}
	ip, err := vpnprobe.GatewayIP(subnet)
	if err != nil {
		return VPNProbeStatusView{}, model.ErrPlatform.WithError(err).WithMessage("Invalid team VPN gateway").Err()
	}
	return VPNProbeStatusView{GatewayIP: ip, ProbeURL: fmt.Sprintf("http://%s:%d/", ip, vpnprobe.Port)}, nil
}

func (u *EventUseCase) requireOwnVPNGroup(ctx context.Context, eventID, userID uuid.UUID) (string, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return "", participantModel.ErrParticipantAccessForbidden.Err()
		}
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return "", participantModel.ErrParticipantAccessForbidden.Err()
	}
	if err = requireTeamAdmitted(ctx, u.teams, eventID, *p.TeamID); err != nil {
		return "", err
	}
	if err = requireTeamFormed(ctx, u.teams, eventID, *p.TeamID); err != nil {
		return "", err
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if !e.InfrastructureAllowed {
		return "", participantModel.ErrParticipantAccessForbidden.Err()
	}
	group, err := labBindingModel.GroupName(eventID, *p.TeamID)
	if err != nil {
		return "", err
	}
	return group, nil
}

func participantLabClientName(userID uuid.UUID) string {
	return labBindingModel.ParticipantClientName(userID)
}

// participantVPNLocks serializes the client provisioning of one participant within this process.
var participantVPNLocks = keyedLocks{held: map[string]*keyedLock{}}

type keyedLock struct {
	mu   sync.Mutex
	refs int
}

type keyedLocks struct {
	mu   sync.Mutex
	held map[string]*keyedLock
}

// lock takes the lock of key and returns its release.
func (k *keyedLocks) lock(key string) func() {
	k.mu.Lock()
	l := k.held[key]
	if l == nil {
		l = &keyedLock{}
		k.held[key] = l
	}
	l.refs++
	k.mu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		k.mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(k.held, key)
		}
		k.mu.Unlock()
	}
}
