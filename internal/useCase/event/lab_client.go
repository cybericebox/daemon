package event

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
)

// labClientDeleter and vpnConfigDeleter are optional capabilities of the agent
// port and the VPN store, asserted at call time so their fakes stay as they are.
type labClientDeleter interface {
	DeleteLabClient(ctx context.Context, group, client string) error
}

type vpnConfigDeleter interface {
	DeleteConfig(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID) error
}

// dropMemberLabClient removes the LabGroupClient of a person who left a team (or
// the event) together with the stored encrypted config, so a person who joins
// another team gets a fresh client in that team's group. It is best effort after
// the roster change is saved: the access policy already revokes the person, and
// a failure only leaves an unused client behind.
func (u *EventUseCase) dropMemberLabClient(ctx context.Context, eventID, teamID, userID uuid.UUID) {
	if deleter, ok := u.vpn.(vpnConfigDeleter); ok {
		if err := deleter.DeleteConfig(ctx, userID, vpnModel.ScopeEvent, uuid.NullUUID{UUID: eventID, Valid: true}); err != nil {
			log.Warn().Err(err).Stringer("event_id", eventID).Msg("lab client: stored config was not removed")
		}
	}
	deleter, ok := u.infra.(labClientDeleter)
	if !ok {
		return
	}
	group, err := labBindingModel.GroupName(eventID, teamID)
	if err != nil {
		return
	}
	if err = deleter.DeleteLabClient(ctx, group, participantLabClientName(userID)); err != nil {
		log.Warn().Err(err).Stringer("event_id", eventID).Str("group", group).Msg("lab client: client was not deleted")
	}
}
