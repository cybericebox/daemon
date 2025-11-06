package laboratoryModel

import (
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

type (
	VPNClient struct {
		IDs       VPNClientIDs
		IP        string
		PublicKey string
		DestCIDRs []string
		Banned    bool
		LastSeen  time.Time
	}

	VPNClientIDs struct {
		UserID    uuid.UUID
		TeamID    uuid.UUID
		EventID   uuid.UUID
		CustomIDs []string
	}
)

const (
	eventIDName = "event"
	teamIDName  = "team"
	userIDName  = "user"
)

func ParseClientIDs(ids []string) VPNClientIDs {
	parsedIds := VPNClientIDs{}
	for _, id := range ids {
		switch {
		case strings.HasPrefix(id, userIDName+":"):
			id = strings.TrimPrefix(id, userIDName+":")
			parsedIds.UserID = uuid.FromStringOrNil(id)
			break
		case strings.HasPrefix(id, teamIDName+":"):
			id = strings.TrimPrefix(id, teamIDName+":")
			parsedIds.TeamID = uuid.FromStringOrNil(id)
			break
		case strings.HasPrefix(id, eventIDName+":"):
			id = strings.TrimPrefix(id, eventIDName+":")
			parsedIds.EventID = uuid.FromStringOrNil(id)
			break
		default:
			parsedIds.CustomIDs = append(parsedIds.CustomIDs, id)
		}

	}
	return parsedIds
}

func (ids VPNClientIDs) StringIDS() []string {
	var idsList []string
	if ids.UserID != uuid.Nil {
		idsList = append(idsList, userIDName+":"+ids.UserID.String())
	}
	if ids.TeamID != uuid.Nil {
		idsList = append(idsList, teamIDName+":"+ids.TeamID.String())
	}
	if ids.EventID != uuid.Nil {
		idsList = append(idsList, eventIDName+":"+ids.EventID.String())
	}
	idsList = append(idsList, ids.CustomIDs...)
	return idsList
}
