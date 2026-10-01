// Package eventAnalytics is the event analytics application layer
// (docs/EVENT-ANALYTICS.md): the access rules of §7 for the analytics API,
// the report reads behind a short shared cache, and the rollup job that
// keeps the 5-minute buckets and VPN sessions up to date.
package eventAnalytics

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
)

type (
	// Store is the analytics statement port (satisfied by
	// *eventAnalyticsRepo.Repository).
	Store interface {
		ParticipantStore
		StandStore
		UsageStore
		IntegrityStore
		ReportStore
		RollupCandidates(ctx context.Context, now time.Time, limit int32) ([]eventAnalyticsRepo.RollupCandidate, error)
		RefreshBuckets(ctx context.Context, eventID uuid.UUID) error
		MarkBucketsRefreshed(ctx context.Context, eventID uuid.UUID, at time.Time, revision int64, finalizedAt *time.Time) error
		VPNSamples(ctx context.Context, eventID uuid.UUID, after eventAnalyticsRepo.VPNCursor, batchSize int32) ([]eventAnalyticsModel.VPNSample, eventAnalyticsRepo.VPNCursor, int, error)
		OpenVPNSessions(ctx context.Context, eventID uuid.UUID, endedAfter time.Time) ([]eventAnalyticsModel.VPNSession, error)
		SaveVPNSessions(ctx context.Context, eventID uuid.UUID, changed []eventAnalyticsModel.VPNSession, absorbed []uuid.UUID) error
		SetVPNCursor(ctx context.Context, eventID uuid.UUID, cursor eventAnalyticsRepo.VPNCursor, finalizedAt *time.Time) error
		Overview(ctx context.Context, eventID uuid.UUID, activeSince time.Time) (eventAnalyticsRepo.Overview, error)
		Series(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.SeriesPoint, error)
		RollupState(ctx context.Context, eventID uuid.UUID) (eventAnalyticsRepo.RollupState, error)
		TaskStore
		Feed(ctx context.Context, eventID uuid.UUID, limit int32) ([]eventAnalyticsRepo.FeedItem, error)
	}

	// Events reads an event (satisfied by *eventRepo.Repository).
	Events interface {
		GetByID(ctx context.Context, id uuid.UUID) (eventModel.Event, error)
	}

	// Configs reads an event config (satisfied by *eventConfigRepo.Repository).
	Configs interface {
		Get(ctx context.Context, eventID uuid.UUID) (eventConfigModel.EventConfig, error)
	}

	// Memberships reads a viewer's role in an event (satisfied by
	// *eventManagerRepo.Repository).
	Memberships interface {
		Get(ctx context.Context, eventID, userID uuid.UUID) (eventManagerModel.EventManager, error)
	}

	Dependencies struct {
		Store       Store
		Events      Events
		Configs     Configs
		Memberships Memberships
		// LabTraffic answers whether a team touched a task's lab (satisfied by
		// *labTrafficRepo.Repository); nil means the VPN sessions decide.
		LabTraffic LabTraffic
		// Now is the clock; nil means time.Now.
		Now func() time.Time
		// NewID makes VPN session IDs; nil means UUIDv7.
		NewID func() uuid.UUID
	}

	EventAnalyticsUseCase struct {
		store       Store
		events      Events
		configs     Configs
		memberships Memberships
		labTraffic  LabTraffic
		cache       *reportCache
		now         func() time.Time
		newID       func() uuid.UUID
	}
)

func New(deps Dependencies) *EventAnalyticsUseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	newID := deps.NewID
	if newID == nil {
		newID = func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	}
	return &EventAnalyticsUseCase{
		store: deps.Store, events: deps.Events, configs: deps.Configs, memberships: deps.Memberships, labTraffic: deps.LabTraffic,
		cache: newReportCache(now), now: now, newID: newID,
	}
}
