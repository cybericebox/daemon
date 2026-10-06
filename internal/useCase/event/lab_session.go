package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

// LabSessionIssuer signs the handoff link of the laboratory L7 proxy.
type LabSessionIssuer interface {
	Issue(context.Context, labaccess.Session, time.Time) (labaccess.Link, error)
}

// OpenOwnLabLink returns the link that opens one web device of the caller's task
// lab. The participant must be an approved member of an admitted team that has
// the task published, and the event must have laboratories. The link
// (https://<device>-<code>.<base>/_auth?t=...) carries the team's lab group and
// the participant's client, nothing else; the proxy turns it into its own cookie
// for the lab domain, so the platform sets no cookie. The link lives about two
// minutes and is fetched fresh on every click; the session it grants lasts until
// the event's effective finish plus a small buffer (the issuer's TTL is only the
// fallback) and is never extended. Blocking a person is done by the group access
// policy, not by the link.
func (u *EventUseCase) OpenOwnLabLink(ctx context.Context, eventID, userID, challengeID uuid.UUID, device string, port int32) (labaccess.Link, error) {
	if u.labSessions == nil {
		return labaccess.Link{}, infraUnavailable()
	}
	p, err := u.requireOwnAvailableChallenge(ctx, eventID, userID, challengeID)
	if err != nil {
		return labaccess.Link{}, err
	}
	if _, err = u.requireEventLaboratories(ctx, eventID); err != nil {
		return labaccess.Link{}, err
	}
	binding, err := u.labBindings.Get(ctx, *p.TeamID, challengeID)
	if err != nil {
		return labaccess.Link{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get lab binding").Err()
	}
	if err = u.requireMemberLabClient(ctx, eventID, userID); err != nil {
		return labaccess.Link{}, err
	}
	return u.issueLabLink(ctx, eventID, userID, binding, device, port)
}

// OpenModeratorsLabLink returns the handoff link of a manager for a device of a
// task of the hidden moderators team, so staff reach the labs they test like any
// team does. The route gate admits the owner and write moderators; the link names
// the manager's client and the moderators team's lab group, and the proxy applies
// the same group access policy as for participants.
func (u *EventUseCase) OpenModeratorsLabLink(ctx context.Context, eventID, userID, challengeID uuid.UUID, device string, port int32) (labaccess.Link, error) {
	if u.labSessions == nil {
		return labaccess.Link{}, infraUnavailable()
	}
	if _, err := u.requireEventLaboratories(ctx, eventID); err != nil {
		return labaccess.Link{}, err
	}
	teamID, err := u.moderatorsTeam(ctx, eventID)
	if err != nil {
		return labaccess.Link{}, err
	}
	binding, err := u.labBindings.Get(ctx, teamID, challengeID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return labaccess.Link{}, eventStandModel.ErrStandLabNotFound.Err()
		}
		return labaccess.Link{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get lab binding").Err()
	}
	// Staff clients of the moderators team are created by the access sync when a
	// manager is assigned, never here.
	if err = u.requireMemberLabClient(ctx, eventID, userID); err != nil {
		return labaccess.Link{}, err
	}
	return u.issueLabLink(ctx, eventID, userID, binding, device, port)
}

// issueLabLink finds the device's web address in the live lab status and signs
// the handoff for it.
func (u *EventUseCase) issueLabLink(ctx context.Context, eventID, userID uuid.UUID, binding labBindingModel.Binding, device string, port int32) (labaccess.Link, error) {
	if u.infra == nil {
		return labaccess.Link{}, infraUnavailable()
	}
	status, err := u.infra.LabStatus(ctx, binding.LabGroupName, binding.LabName)
	if err != nil {
		return labaccess.Link{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get lab status").Err()
	}
	accessURL, ok := status.WebURL(device, port)
	if !ok {
		return labaccess.Link{}, eventStandModel.ErrStandLabNoWebDevice.Err()
	}
	now := time.Now()
	expiresAt, err := u.labSessionExpiry(ctx, eventID, now)
	if err != nil {
		return labaccess.Link{}, err
	}
	link, err := u.labSessions.Issue(ctx, labaccess.Session{Group: binding.LabGroupName, Client: participantLabClientName(userID), AccessURL: accessURL, ExpiresAt: expiresAt}, now)
	if err != nil {
		return labaccess.Link{}, model.ErrPlatform.WithError(err).WithMessage("Failed to issue the laboratory web link").Err()
	}
	return link, nil
}

// labSessionFinishBuffer keeps the session valid a little past the event's finish
// so a request in flight at the finish does not fail on the session.
const labSessionFinishBuffer = time.Hour

// labSessionExpiry is the wanted end of a lab session: the event's effective
// finish plus the buffer. It is zero (the issuer's TTL applies) for an event
// without a finish or whose finish has passed.
func (u *EventUseCase) labSessionExpiry(ctx context.Context, eventID uuid.UUID, now time.Time) (time.Time, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return time.Time{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return labSessionExpiryAt(e.Lifecycle.EffectiveFinishAt(), now), nil
}

func labSessionExpiryAt(finish *time.Time, now time.Time) time.Time {
	if finish == nil {
		return time.Time{}
	}
	if expires := finish.Add(labSessionFinishBuffer); expires.After(now) {
		return expires
	}
	return time.Time{}
}

// EnsureMemberLabClient creates the LabGroupClient of one member in their team's
// lab group, once, and keeps its VPN config encrypted in the platform store. It
// is the only way a client comes to exist: the team formation calls it for every
// member in one go, and it is called again for a person added later. It is
// idempotent: a member whose config is already stored is left alone (the agent
// returns a complete config only at creation, so an existing client is never
// recreated). The client is the VPN peer and the proxy identity at once; only
// the access config (VPN config, web session) is handed out on demand.
func (u *EventUseCase) EnsureMemberLabClient(ctx context.Context, eventID, teamID, userID uuid.UUID) error {
	if u.vpn == nil || u.infra == nil {
		return infraUnavailable()
	}
	group, err := labBindingModel.GroupName(eventID, teamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to derive team lab group").Err()
	}
	_, err = ensureParticipantVPNConfig(ctx, u.vpn, u.infra, group, eventID, userID)
	return err
}

// requireMemberLabClient refuses a session for a member whose client was not
// created yet: a token for a missing client would be refused by the proxy anyway.
func (u *EventUseCase) requireMemberLabClient(ctx context.Context, eventID, userID uuid.UUID) error {
	if u.vpn != nil {
		config, err := u.vpn.GetConfig(ctx, userID, vpnModel.ScopeEvent, uuid.NullUUID{UUID: eventID, Valid: true})
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to read participant laboratory client").Err()
		}
		if config != "" {
			return nil
		}
	}
	return eventStandModel.ErrStandLabClientMissing.Err()
}
