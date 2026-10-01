package eventAnalytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

type usageFixture struct {
	users      []eventAnalyticsRepo.UsageUser
	vpn        []eventAnalyticsRepo.UsageVPN
	sessions   []eventAnalyticsRepo.UsageSession
	handshakes []eventAnalyticsRepo.UsageHandshake
	touches    []eventAnalyticsRepo.UsageTouch
	team       *uuid.UUID
}

// Default (empty) answers of the «Використання» reads for the shared fakeStore.
func (s *fakeStore) UsageUsers(context.Context, uuid.UUID, *uuid.UUID) ([]eventAnalyticsRepo.UsageUser, error) {
	return nil, nil
}
func (s *fakeStore) UsageVPN(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.UsageVPN, error) {
	return nil, nil
}
func (s *fakeStore) UsageSessions(context.Context, uuid.UUID, eventAnalyticsModel.Period, int32) ([]eventAnalyticsRepo.UsageSession, error) {
	return nil, nil
}
func (s *fakeStore) UsageHandshakes(context.Context, uuid.UUID) ([]eventAnalyticsRepo.UsageHandshake, error) {
	return nil, nil
}
func (s *fakeStore) UsageTouches(context.Context, uuid.UUID) ([]eventAnalyticsRepo.UsageTouch, error) {
	return nil, nil
}

func (s *sectionsStore) UsageUsers(_ context.Context, _ uuid.UUID, team *uuid.UUID) ([]eventAnalyticsRepo.UsageUser, error) {
	s.usage.team = team
	return s.usage.users, nil
}
func (s *sectionsStore) UsageVPN(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.UsageVPN, error) {
	return s.usage.vpn, nil
}
func (s *sectionsStore) UsageSessions(context.Context, uuid.UUID, eventAnalyticsModel.Period, int32) ([]eventAnalyticsRepo.UsageSession, error) {
	return s.usage.sessions, nil
}
func (s *sectionsStore) UsageHandshakes(context.Context, uuid.UUID) ([]eventAnalyticsRepo.UsageHandshake, error) {
	return s.usage.handshakes, nil
}
func (s *sectionsStore) UsageTouches(context.Context, uuid.UUID) ([]eventAnalyticsRepo.UsageTouch, error) {
	return s.usage.touches, nil
}

func TestGetEventAnalyticsUsage_NoInfrastructureIsUnavailable(t *testing.T) {
	clock := now
	event := runningEvent()
	uc := newUC(&fakeStore{}, event, nil, &clock)

	v, err := uc.GetEventAnalyticsUsage(context.Background(), event.ID, nil, nil, nil)
	require.NoError(t, err)
	require.False(t, v.Available)
	require.Empty(t, v.Users)
}

func TestGetEventAnalyticsUsage_OnlineFollowsTheLiveHandshakeOnly(t *testing.T) {
	clock := now
	event := runningEvent()
	event.InfrastructureAllowed = true
	team := uuid.UUID{15: 9}
	online, stale, never, sessionOnly := uuid.UUID{15: 1}, uuid.UUID{15: 2}, uuid.UUID{15: 3}, uuid.UUID{15: 4}
	store := &sectionsStore{fakeStore: &fakeStore{}, usage: usageFixture{
		users: []eventAnalyticsRepo.UsageUser{
			{UserID: never, TeamID: team, TeamName: "T", UserName: "Zed"},
			{UserID: stale, TeamID: team, TeamName: "T", UserName: "Bob"},
			{UserID: online, TeamID: team, TeamName: "T", UserName: "Ann"},
			{UserID: sessionOnly, TeamID: team, TeamName: "T", UserName: "Cid"},
		},
		handshakes: []eventAnalyticsRepo.UsageHandshake{
			{UserID: online, Handshake: clock.Add(-90 * time.Second)},
			// Bytes flowed recently, but the handshake is past the window.
			{UserID: stale, Handshake: clock.Add(-eventAnalyticsModel.VPNOnlineWindow - time.Minute)},
		},
		vpn: []eventAnalyticsRepo.UsageVPN{
			{UserID: online, Sessions: 2, Seconds: 600, RxBytes: 100, TxBytes: 10, FirstAt: clock.Add(-time.Hour), LastAt: clock.Add(-2 * time.Minute)},
			{UserID: stale, Sessions: 1, Seconds: 60, RxBytes: 50, TxBytes: 5, FirstAt: clock.Add(-time.Hour), LastAt: clock.Add(-30 * time.Minute)},
			// The rolled-up session is fresh; the live state is missing.
			{UserID: sessionOnly, Sessions: 1, Seconds: 0, FirstAt: clock.Add(-time.Minute), LastAt: clock.Add(-time.Minute)},
		},
		sessions: []eventAnalyticsRepo.UsageSession{
			{UserID: online, StartedAt: clock.Add(-10 * time.Minute), EndedAt: clock.Add(-2 * time.Minute), RxBytes: 70, TxBytes: 7},
		},
		touches: []eventAnalyticsRepo.UsageTouch{
			{UserID: online, ChallengeID: uuid.UUID{15: 7}, Task: "Web 1", Surface: "proxy", Attempts: 12, BytesIn: 1000, BytesOut: 200,
				FirstSeenAt: clock.Add(-50 * time.Minute), LastSeenAt: clock.Add(-5 * time.Minute)},
			{UserID: online, ChallengeID: uuid.UUID{15: 8}, Task: "Web 2", Surface: "proxy", Attempts: 3, BytesIn: 10, BytesOut: 20,
				FirstSeenAt: clock.Add(-70 * time.Minute), LastSeenAt: clock.Add(-1 * time.Minute)},
			{UserID: online, ChallengeID: uuid.UUID{15: 7}, Task: "Web 1", Surface: "vpn", Attempts: 5, BytesIn: 99, BytesOut: 99,
				FirstSeenAt: clock.Add(-40 * time.Minute), LastSeenAt: clock.Add(-3 * time.Minute)},
		},
	}}
	uc := newUCWithStore(store, event, &clock)

	v, err := uc.GetEventAnalyticsUsage(context.Background(), event.ID, nil, nil, nil)
	require.NoError(t, err)
	require.True(t, v.Available)

	names := []string{}
	for _, u := range v.Users {
		names = append(names, u.UserName)
	}
	// Online first, then the freshest handshake, never-connected last.
	require.Equal(t, []string{"Cid", "Ann", "Bob", "Zed"}, names)

	cid, ann, bob, zed := v.Users[0], v.Users[1], v.Users[2], v.Users[3]
	require.True(t, ann.VPN.Online)
	require.True(t, cid.VPN.Online, "a fresh rolled-up session counts when the live state is missing")
	require.False(t, bob.VPN.Online, "an old handshake is offline however much traffic there was")
	require.False(t, zed.VPN.Online)
	require.Nil(t, zed.VPN.LastHandshakeAt)
	require.Nil(t, zed.VPN.FirstAt)

	require.Equal(t, clock.Add(-90*time.Second), *ann.VPN.LastHandshakeAt, "the live handshake is newer than the session end")
	require.EqualValues(t, 2, ann.VPN.Sessions)
	require.EqualValues(t, 600, ann.VPN.Seconds)
	require.Len(t, ann.VPN.Recent, 1)
	require.EqualValues(t, 480, ann.VPN.Recent[0].Seconds)

	require.EqualValues(t, 15, ann.Proxy.Requests, "only proxy rows feed the proxy totals")
	require.EqualValues(t, 1010, ann.Proxy.BytesIn)
	require.EqualValues(t, 220, ann.Proxy.BytesOut)
	require.Equal(t, clock.Add(-70*time.Minute), *ann.Proxy.FirstAt)
	require.Equal(t, clock.Add(-1*time.Minute), *ann.Proxy.LastAt)
	require.Len(t, ann.Labs, 3, "the per-task list keeps both access types")
	require.Nil(t, zed.Proxy.FirstAt)

	s := v.Summary
	require.EqualValues(t, 4, s.Users)
	require.EqualValues(t, 2, s.OnlineNow)
	require.EqualValues(t, 3, s.VPNUsers)
	require.EqualValues(t, 1, s.ProxyUsers)
	require.EqualValues(t, 4, s.Sessions)
	require.EqualValues(t, 660, s.OnlineSeconds)
	require.EqualValues(t, 15, s.ProxyRequests)
	require.EqualValues(t, 1230, s.ProxyBytes)
}

func TestGetEventAnalyticsUsage_PassesTheTeamFilter(t *testing.T) {
	clock := now
	event := runningEvent()
	event.InfrastructureAllowed = true
	store := &sectionsStore{fakeStore: &fakeStore{}}
	uc := newUCWithStore(store, event, &clock)
	team := uuid.UUID{15: 5}

	_, err := uc.GetEventAnalyticsUsage(context.Background(), event.ID, &team, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, store.usage.team)
	require.Equal(t, team, *store.usage.team)
}
