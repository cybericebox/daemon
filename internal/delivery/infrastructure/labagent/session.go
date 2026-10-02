package labagent

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"

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

// warnLimits says once per agent that a configured TTL is above what its proxy accepts; links are capped.
func (s SessionIssuer) warnLimits(member *Member, sk labaccess.SigningKey) {
	if sk.MaxTokenTTL > 0 && s.Issuer.TokenTTL() > sk.MaxTokenTTL {
		if _, warned := s.Fleet.limitWarned.LoadOrStore(member.ID.String()+"/token", true); !warned {
			log.Warn().Str("agent", member.Name).Dur("configured", s.Issuer.TokenTTL()).Dur("proxyMax", sk.MaxTokenTTL).
				Msg("LAB_ACCESS_TOKEN_TTL is above what the agent's proxy accepts: its links are capped")
		}
	}
	if sk.MaxSessionTTL > 0 && s.Issuer.SessionTTL() > sk.MaxSessionTTL {
		if _, warned := s.Fleet.limitWarned.LoadOrStore(member.ID.String()+"/session", true); !warned {
			log.Warn().Str("agent", member.Name).Dur("configured", s.Issuer.SessionTTL()).Dur("proxyMax", sk.MaxSessionTTL).
				Msg("LAB_SESSION_TTL is above what the agent's proxy keeps a session: sessions of this agent are capped")
		}
	}
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
	sk := labaccess.SigningKey{Tenant: member.Tenant, KeyID: member.AccessKeyID, Key: member.AccessKey}
	if f := member.Features.Get(); f != nil {
		sk.MaxTokenTTL = time.Duration(f.Proxy.AccessTokenMaxTTLSeconds) * time.Second
		sk.MaxSessionTTL = time.Duration(f.Proxy.SessionMaxTTLSeconds) * time.Second
		s.warnLimits(member, sk)
	}
	return s.Issuer.Issue(sk, session, now)
}
