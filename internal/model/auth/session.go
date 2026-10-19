package authModel

import (
	"net/netip"
	"strings"
	"time"

	"github.com/gofrs/uuid"
)

// Session represents a session stored in the database.
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	ExpiresAt time.Time
	LastSeen  time.Time
	CreatedAt time.Time
	Metadata  SessionMetadata
}

type SessionMetadata struct {
	UserAgent string `json:"user_agent"`
	// IP is the client's public address, or empty: see SessionIP.
	IP string `json:"ip"`
}

// SessionIP is the address a session may keep and show: the client address in canonical form when it is a
// public unicast address, else "". A private (10/8, 172.16/12, 192.168/16, fc00::/7), CGNAT (100.64/10),
// loopback, link-local, unspecified or multicast address is the ingress, a pod or a NAT, not the client; it is
// never stored nor sent. The caller passes an address it trusts (gin's ClientIP reads X-Forwarded-For only from
// TRUSTED_PROXIES); anything that does not parse as a bare address is dropped too.
func SessionIP(addr string) string {
	ip, err := netip.ParseAddr(strings.TrimSpace(addr))
	if err != nil || ip.Zone() != "" {
		return ""
	}
	ip = ip.Unmap()
	// 100.64.0.0/10 is the shared address space of carrier-grade NAT (RFC 6598); netip does not count it as private.
	const cgnatBits = 10
	cgnat := netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 64, 0, 0}), cgnatBits)
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || cgnat.Contains(ip) {
		return ""
	}
	return ip.String()
}

// NewSession builds a fresh session with domain-owned defaults: the ID
// (UUIDv7) and every timestamp are set here, not in the database — one source
// of truth, and the entity is fully testable without a DB. now is passed in so
// callers (and tests) control the clock.
// Only a public client address is kept (SessionIP).
func NewSession(
	userID uuid.UUID,
	meta SessionMetadata,
	idleTTL time.Duration,
	now time.Time,
) Session {
	meta.IP = SessionIP(meta.IP)
	return Session{
		ID:        uuid.Must(uuid.NewV7()),
		UserID:    userID,
		ExpiresAt: now.Add(idleTTL),
		LastSeen:  now,
		CreatedAt: now,
		Metadata:  meta,
	}
}

// IsExpired reports whether the session's idle deadline has passed.
func (s *Session) IsExpired(now time.Time) bool {
	return now.After(s.ExpiresAt)
}

// ExceedsAbsoluteLifetime reports whether the session is older than absolute (a
// session ends that long after sign-in however busy it is); zero means no limit.
func (s *Session) ExceedsAbsoluteLifetime(absolute time.Duration, now time.Time) bool {
	return absolute > 0 && !now.Before(s.CreatedAt.Add(absolute))
}

// Touch records activity: it extends the idle deadline and moves LastSeen.
func (s *Session) Touch(idleTTL time.Duration, now time.Time) {
	s.ExpiresAt = now.Add(idleTTL)
	s.LastSeen = now
}

// BelongsTo reports whether the session is owned by userID.
func (s *Session) BelongsTo(userID uuid.UUID) bool {
	return s.UserID == userID
}
