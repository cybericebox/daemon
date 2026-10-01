package labagent

import (
	"context"
	"time"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

// SessionIssuer signs the handoff links of the laboratory L7 proxy with the access key of the agent
// that holds the lab group: every agent is a tenant with its own key, the tenant name is the token's
// issuer. A group whose agent has no usable key gets no web link (VPN access is unaffected).
type SessionIssuer struct {
	Fleet  *Fleet
	Issuer *labaccess.Issuer
}

// Issue signs an access link for one device of a lab group.
func (s SessionIssuer) Issue(ctx context.Context, session labaccess.Session, now time.Time) (labaccess.Link, error) {
	member, err := s.Fleet.memberOf(ctx, session.Group)
	if err != nil {
		return labaccess.Link{}, err
	}
	if member.Tenant == "" || member.AccessKeyID == "" || len(member.AccessKey) == 0 {
		return labaccess.Link{}, infraModel.ErrInfrastructureUnavailable.Err()
	}
	return s.Issuer.Issue(labaccess.SigningKey{Tenant: member.Tenant, KeyID: member.AccessKeyID, Key: member.AccessKey}, session, now)
}
