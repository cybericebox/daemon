package eventAnalytics

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	labTrafficModel "github.com/cybericebox/daemon/internal/model/labTraffic"
)

// usageSessionsPerUser bounds the session list of one participant; the totals
// cover all of the sessions.
const usageSessionsPerUser = 20

// UsageStore is the statement port of «Використання».
type UsageStore interface {
	UsageUsers(ctx context.Context, eventID uuid.UUID, teamID *uuid.UUID) ([]eventAnalyticsRepo.UsageUser, error)
	UsageVPN(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.UsageVPN, error)
	UsageSessions(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period, perUser int32) ([]eventAnalyticsRepo.UsageSession, error)
	UsageHandshakes(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.UsageHandshake, error)
	UsageTouches(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.UsageTouch, error)
}

type (
	// UsageView is the «Використання» report: per participant, the VPN
	// connection state and the lab access over the VPN and the web proxy.
	UsageView struct {
		// Available is false for an event without infrastructure.
		Available bool
		Summary   UsageSummaryView
		Users     []UsageUserView
		Period    PeriodView
		// At is the moment the online state is measured at.
		At time.Time
	}

	UsageSummaryView struct {
		Users int64
		// OnlineNow are the participants whose VPN handshake is within the
		// online window.
		OnlineNow int64
		// VPNUsers connected at least once in the period; ProxyUsers used the
		// web proxy at least once (any time in the event).
		VPNUsers, ProxyUsers int64
		Sessions             int64
		OnlineSeconds        int64
		RxBytes, TxBytes     int64
		ProxyRequests        int64
		ProxyBytes           int64
	}

	UsageUserView struct {
		UserID   uuid.UUID
		UserName string
		TeamID   uuid.UUID
		TeamName string
		// LastSeenAt is the last request on the event, LastLabAt the last lab
		// access over the VPN or the proxy; nil when never.
		LastSeenAt, LastLabAt *time.Time
		VPN                   UsageVPNView
		Proxy                 UsageProxyView
		// Labs are the per-task counters, VPN and proxy rows together.
		Labs []UsageLabView
	}

	// UsageVPNView: Online comes from the last handshake alone, never from
	// traffic. Seconds is the time between the first and the last handshake of
	// each session, summed.
	UsageVPNView struct {
		Online          bool
		LastHandshakeAt *time.Time
		FirstAt         *time.Time
		Sessions        int64
		Seconds         int64
		RxBytes         int64
		TxBytes         int64
		// Recent are the latest sessions, newest first.
		Recent []UsageSessionView
	}

	UsageSessionView struct {
		StartedAt, EndedAt time.Time
		Seconds            int64
		RxBytes, TxBytes   int64
	}

	// UsageProxyView sums the participant's web proxy use over the event.
	UsageProxyView struct {
		Requests          int64
		BytesIn, BytesOut int64
		FirstAt, LastAt   *time.Time
	}

	UsageLabView struct {
		ChallengeID       uuid.UUID
		Task              string
		Surface           string
		Attempts          int64
		BytesIn, BytesOut int64
		FirstAt, LastAt   time.Time
	}
)

// GetEventAnalyticsUsage is the «Використання» report. The period bounds the
// VPN sessions; the lab counters are cumulative and cover the whole event.
// teamID narrows it to one team. It is not cached: «online now» is live.
func (u *EventAnalyticsUseCase) GetEventAnalyticsUsage(ctx context.Context, eventID uuid.UUID, teamID *uuid.UUID, from, to *time.Time) (UsageView, error) {
	e, err := u.event(ctx, eventID)
	if err != nil {
		return UsageView{}, err
	}
	if !e.InfrastructureAllowed {
		return UsageView{}, nil
	}
	now := u.now()
	period, err := eventAnalyticsModel.NewPeriod(from, to, e.Lifecycle.StartAt, e.Lifecycle.EffectiveFinishAt(), now)
	if err != nil {
		return UsageView{}, err
	}
	fail := func(err error, what string) (UsageView, error) {
		return UsageView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read " + what).Err()
	}
	users, err := u.store.UsageUsers(ctx, eventID, teamID)
	if err != nil {
		return fail(err, "event usage participants")
	}
	vpn, err := u.store.UsageVPN(ctx, eventID, period)
	if err != nil {
		return fail(err, "event VPN usage")
	}
	sessions, err := u.store.UsageSessions(ctx, eventID, period, usageSessionsPerUser)
	if err != nil {
		return fail(err, "event VPN sessions")
	}
	handshakes, err := u.store.UsageHandshakes(ctx, eventID)
	if err != nil {
		return fail(err, "event VPN handshakes")
	}
	touches, err := u.store.UsageTouches(ctx, eventID)
	if err != nil {
		return fail(err, "event lab access")
	}
	view := buildUsage(users, vpn, sessions, handshakes, touches, now)
	view.Period = PeriodView{From: period.From, To: period.To}
	return view, nil
}

func buildUsage(users []eventAnalyticsRepo.UsageUser, vpn []eventAnalyticsRepo.UsageVPN, sessions []eventAnalyticsRepo.UsageSession,
	handshakes []eventAnalyticsRepo.UsageHandshake, touches []eventAnalyticsRepo.UsageTouch, now time.Time) UsageView {
	vpnOf := map[uuid.UUID]eventAnalyticsRepo.UsageVPN{}
	for _, v := range vpn {
		vpnOf[v.UserID] = v
	}
	sessionsOf := map[uuid.UUID][]eventAnalyticsRepo.UsageSession{}
	for _, s := range sessions {
		sessionsOf[s.UserID] = append(sessionsOf[s.UserID], s)
	}
	liveOf := map[uuid.UUID]time.Time{}
	for _, h := range handshakes {
		if h.Handshake.After(liveOf[h.UserID]) {
			liveOf[h.UserID] = h.Handshake
		}
	}
	touchesOf := map[uuid.UUID][]eventAnalyticsRepo.UsageTouch{}
	for _, t := range touches {
		touchesOf[t.UserID] = append(touchesOf[t.UserID], t)
	}

	view := UsageView{Available: true, At: now, Users: make([]UsageUserView, 0, len(users))}
	s := &view.Summary
	for _, user := range users {
		uv := UsageUserView{UserID: user.UserID, UserName: user.UserName, TeamID: user.TeamID, TeamName: user.TeamName, LastSeenAt: user.LastSeenAt, LastLabAt: user.LastLabAt, Labs: []UsageLabView{}}
		uv.VPN.Recent = []UsageSessionView{}

		last := liveOf[user.UserID]
		if v, ok := vpnOf[user.UserID]; ok {
			first := v.FirstAt
			uv.VPN.FirstAt = &first
			uv.VPN.Sessions, uv.VPN.Seconds, uv.VPN.RxBytes, uv.VPN.TxBytes = v.Sessions, v.Seconds, v.RxBytes, v.TxBytes
			if v.LastAt.After(last) {
				last = v.LastAt
			}
			s.VPNUsers++
			s.Sessions += v.Sessions
			s.OnlineSeconds += v.Seconds
			s.RxBytes += v.RxBytes
			s.TxBytes += v.TxBytes
		}
		if !last.IsZero() {
			at := last
			uv.VPN.LastHandshakeAt = &at
		}
		uv.VPN.Online = eventAnalyticsModel.VPNOnline(last, now)
		if uv.VPN.Online {
			s.OnlineNow++
		}
		for _, session := range sessionsOf[user.UserID] {
			uv.VPN.Recent = append(uv.VPN.Recent, UsageSessionView{
				StartedAt: session.StartedAt, EndedAt: session.EndedAt, Seconds: int64(session.EndedAt.Sub(session.StartedAt).Seconds()),
				RxBytes: session.RxBytes, TxBytes: session.TxBytes,
			})
		}

		for _, t := range touchesOf[user.UserID] {
			uv.Labs = append(uv.Labs, UsageLabView{
				ChallengeID: t.ChallengeID, Task: t.Task, Surface: t.Surface, Attempts: t.Attempts,
				BytesIn: t.BytesIn, BytesOut: t.BytesOut, FirstAt: t.FirstSeenAt, LastAt: t.LastSeenAt,
			})
			if t.Surface != string(labTrafficModel.SurfaceProxy) {
				continue
			}
			p := &uv.Proxy
			p.Requests += t.Attempts
			p.BytesIn += t.BytesIn
			p.BytesOut += t.BytesOut
			if p.FirstAt == nil || t.FirstSeenAt.Before(*p.FirstAt) {
				first := t.FirstSeenAt
				p.FirstAt = &first
			}
			if p.LastAt == nil || t.LastSeenAt.After(*p.LastAt) {
				lastSeen := t.LastSeenAt
				p.LastAt = &lastSeen
			}
		}
		if uv.Proxy.Requests > 0 {
			s.ProxyUsers++
			s.ProxyRequests += uv.Proxy.Requests
			s.ProxyBytes += uv.Proxy.BytesIn + uv.Proxy.BytesOut
		}
		view.Users = append(view.Users, uv)
	}
	s.Users = int64(len(users))

	// Online first, then the most recent handshake, then the name.
	sort.SliceStable(view.Users, func(i, j int) bool {
		a, b := view.Users[i], view.Users[j]
		if a.VPN.Online != b.VPN.Online {
			return a.VPN.Online
		}
		switch {
		case a.VPN.LastHandshakeAt != nil && b.VPN.LastHandshakeAt != nil && !a.VPN.LastHandshakeAt.Equal(*b.VPN.LastHandshakeAt):
			return a.VPN.LastHandshakeAt.After(*b.VPN.LastHandshakeAt)
		case (a.VPN.LastHandshakeAt != nil) != (b.VPN.LastHandshakeAt != nil):
			return a.VPN.LastHandshakeAt != nil
		}
		return strings.ToLower(a.UserName) < strings.ToLower(b.UserName)
	})
	return view
}
