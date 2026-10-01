package eventAnalyticsModel

import (
	"sort"
	"time"

	"github.com/gofrs/uuid"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// VPNOnlineWindow is how recent the last handshake must be for a peer to count
// as online. WireGuard renews the handshake every two minutes while a peer is
// in use, so the window is that plus a margin. It is the same threshold the
// test-lab indicator uses; connection state is never read from traffic.
const VPNOnlineWindow = 3 * time.Minute

// VPNSessionGap separates two VPN sessions of one client: handshakes further
// apart than the online window mean the peer was offline in between.
const VPNSessionGap = VPNOnlineWindow

// VPNOnline reports whether a peer whose last handshake was at last is online
// at now. A zero time (never connected) is offline.
func VPNOnline(last, now time.Time) bool {
	return !last.IsZero() && last.Unix() > 0 && now.Sub(last) <= VPNOnlineWindow
}

// VPNSample is one peer statistic of one observation: the last handshake and
// the cumulative transfer counters at that moment.
type VPNSample struct {
	TeamID     uuid.UUID
	Client     string
	ObservedAt time.Time
	Handshake  time.Time
	Rx, Tx     int64
}

// VPNSession is a run of handshakes of one client without a gap over
// VPNSessionGap. Transfer is the counter growth seen inside it.
type VPNSession struct {
	ID        uuid.UUID
	TeamID    uuid.UUID
	Client    string
	UserID    *uuid.UUID
	StartedAt time.Time
	EndedAt   time.Time
	RxMin     int64
	RxMax     int64
	TxMin     int64
	TxMax     int64
}

// RxBytes and TxBytes are the bytes transferred during the session.
func (s *VPNSession) RxBytes() int64 { return s.RxMax - s.RxMin }
func (s *VPNSession) TxBytes() int64 { return s.TxMax - s.TxMin }

// ClientUser returns the participant a lab client belongs to.
func ClientUser(client string) (uuid.UUID, bool) {
	return labBindingModel.ParseParticipantClientName(client)
}

// MergeVPNSessions folds samples into the known sessions of their clients
// and returns the sessions to store and the IDs of sessions absorbed into
// another one (to delete). A sample without a handshake (never connected)
// is ignored. The merge is idempotent: folding a sample twice changes
// nothing, so a re-read of the same observations is harmless.
func MergeVPNSessions(known []VPNSession, samples []VPNSample, newID func() uuid.UUID) (changed []VPNSession, absorbed []uuid.UUID) {
	type key struct {
		team   uuid.UUID
		client string
	}
	byClient := map[key][]*VPNSession{}
	stored := map[uuid.UUID]bool{}
	for i := range known {
		s := known[i]
		k := key{s.TeamID, s.Client}
		byClient[k] = append(byClient[k], &s)
		stored[s.ID] = true
	}
	touched := map[key]bool{}
	dirty := map[uuid.UUID]bool{}
	for _, sample := range samples {
		if sample.Client == "" || sample.Handshake.IsZero() || sample.Handshake.Unix() <= 0 {
			continue
		}
		k := key{sample.TeamID, sample.Client}
		touched[k] = true
		var target *VPNSession
		for _, s := range byClient[k] {
			if !sample.Handshake.Before(s.StartedAt.Add(-VPNSessionGap)) && !sample.Handshake.After(s.EndedAt.Add(VPNSessionGap)) {
				target = s
				break
			}
		}
		if target == nil {
			target = &VPNSession{
				ID: newID(), TeamID: sample.TeamID, Client: sample.Client,
				StartedAt: sample.Handshake, EndedAt: sample.Handshake,
				RxMin: sample.Rx, RxMax: sample.Rx, TxMin: sample.Tx, TxMax: sample.Tx,
			}
			if userID, ok := ClientUser(sample.Client); ok {
				target.UserID = &userID
			}
			byClient[k] = append(byClient[k], target)
			dirty[target.ID] = true
			continue
		}
		if target.absorb(sample) {
			dirty[target.ID] = true
		}
	}
	for k := range touched {
		sessions := byClient[k]
		sort.Slice(sessions, func(i, j int) bool { return sessions[i].StartedAt.Before(sessions[j].StartedAt) })
		current := sessions[0]
		for _, next := range sessions[1:] {
			if next.StartedAt.After(current.EndedAt.Add(VPNSessionGap)) {
				current = next
				continue
			}
			current.join(*next)
			dirty[current.ID] = true
			delete(dirty, next.ID)
			if stored[next.ID] {
				absorbed = append(absorbed, next.ID)
			}
			next.ID = uuid.Nil
		}
		for _, s := range sessions {
			if s.ID != uuid.Nil && dirty[s.ID] {
				changed = append(changed, *s)
			}
		}
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i].StartedAt.Before(changed[j].StartedAt) })
	return changed, absorbed
}

// absorb widens the session with a sample; it reports whether anything changed.
func (s *VPNSession) absorb(sample VPNSample) bool {
	before := *s
	if sample.Handshake.Before(s.StartedAt) {
		s.StartedAt = sample.Handshake
	}
	if sample.Handshake.After(s.EndedAt) {
		s.EndedAt = sample.Handshake
	}
	s.RxMin, s.RxMax = min(s.RxMin, sample.Rx), max(s.RxMax, sample.Rx)
	s.TxMin, s.TxMax = min(s.TxMin, sample.Tx), max(s.TxMax, sample.Tx)
	return *s != before
}

// join merges another session of the same client into this one.
func (s *VPNSession) join(other VPNSession) {
	if other.StartedAt.Before(s.StartedAt) {
		s.StartedAt = other.StartedAt
	}
	if other.EndedAt.After(s.EndedAt) {
		s.EndedAt = other.EndedAt
	}
	s.RxMin, s.RxMax = min(s.RxMin, other.RxMin), max(s.RxMax, other.RxMax)
	s.TxMin, s.TxMax = min(s.TxMin, other.TxMin), max(s.TxMax, other.TxMax)
}
