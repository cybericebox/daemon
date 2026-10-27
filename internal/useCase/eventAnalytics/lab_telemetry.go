package eventAnalytics

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// Row limits of one lab telemetry read; a read that reaches one is Truncated.
const (
	labTelemetryDeviceRows     = 20000
	labTelemetryLabRows        = 5000
	labTelemetryClientRows     = 20000
	labTelemetryTransitionRows = 5000
)

// LabTelemetryStore is the statement port of the lab telemetry reads.
type LabTelemetryStore interface {
	TeamDeviceMinutes(ctx context.Context, eventID, teamID uuid.UUID, period eventAnalyticsModel.Period, limit int32) ([]eventAnalyticsRepo.DeviceMinute, error)
	TeamLabMinutes(ctx context.Context, eventID, teamID uuid.UUID, period eventAnalyticsModel.Period, limit int32) ([]eventAnalyticsRepo.LabMinute, error)
	TeamClientMinutes(ctx context.Context, eventID, teamID uuid.UUID, period eventAnalyticsModel.Period, limit int32) ([]eventAnalyticsRepo.ClientMinute, error)
	TeamLabTransitions(ctx context.Context, eventID, teamID uuid.UUID, period eventAnalyticsModel.Period, kinds []string, limit int32) ([]eventAnalyticsRepo.LabTransition, error)
}

type (
	// LabTelemetryView is the minute rollups of one team's labs over a period.
	// Available is false for an event without infrastructure.
	LabTelemetryView struct {
		Available bool
		Period    PeriodView
		// Devices, Labs and Clients are ordered by minute; Truncated is set
		// when a list hit its row limit and the period should be narrowed.
		Devices   []eventAnalyticsRepo.DeviceMinute
		Labs      []eventAnalyticsRepo.LabMinute
		Clients   []eventAnalyticsRepo.ClientMinute
		Truncated bool
	}

	// LabTransitionsView is the transitions journal of one team over a period.
	LabTransitionsView struct {
		Available   bool
		Period      PeriodView
		Transitions []eventAnalyticsRepo.LabTransition
		Truncated   bool
	}
)

// LabTransitionKinds are the kinds the journal holds.
var LabTransitionKinds = []string{
	eventAnalyticsModel.LabTransitionLabReady, eventAnalyticsModel.LabTransitionLabNotReady,
	eventAnalyticsModel.LabTransitionDeviceReady, eventAnalyticsModel.LabTransitionDeviceRestart,
	eventAnalyticsModel.LabTransitionPodScheduled, eventAnalyticsModel.LabTransitionPodStarted,
	eventAnalyticsModel.LabTransitionVPNBootChanged, eventAnalyticsModel.LabTransitionPolicyApplied,
	eventAnalyticsModel.LabTransitionClientConnected, eventAnalyticsModel.LabTransitionClientDisconnect,
}

// GetEventLabTelemetry reads the minute rollups (devices, labs, VPN clients) of
// a team in the period. The period defaults to the event and is aligned like
// the other analytics periods.
func (u *EventAnalyticsUseCase) GetEventLabTelemetry(
	ctx context.Context, eventID, teamID uuid.UUID, from, to *time.Time,
) (LabTelemetryView, error) {
	e, err := u.event(ctx, eventID)
	if err != nil {
		return LabTelemetryView{}, err
	}
	if !e.InfrastructureAllowed {
		return LabTelemetryView{}, nil
	}
	period, err := eventAnalyticsModel.NewPeriod(from, to, e.Lifecycle.StartAt, e.Lifecycle.EffectiveFinishAt(), u.now())
	if err != nil {
		return LabTelemetryView{}, err
	}
	fail := func(err error, what string) (LabTelemetryView, error) {
		return LabTelemetryView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read " + what).Err()
	}
	view := LabTelemetryView{Available: true, Period: PeriodView{From: period.From, To: period.To}}
	if view.Devices, err = u.store.TeamDeviceMinutes(ctx, eventID, teamID, period, labTelemetryDeviceRows); err != nil {
		return fail(err, "device minutes")
	}
	if view.Labs, err = u.store.TeamLabMinutes(ctx, eventID, teamID, period, labTelemetryLabRows); err != nil {
		return fail(err, "lab minutes")
	}
	if view.Clients, err = u.store.TeamClientMinutes(ctx, eventID, teamID, period, labTelemetryClientRows); err != nil {
		return fail(err, "VPN client minutes")
	}
	view.Truncated = len(view.Devices) >= labTelemetryDeviceRows || len(view.Labs) >= labTelemetryLabRows ||
		len(view.Clients) >= labTelemetryClientRows
	return view, nil
}

// GetEventLabTransitions reads the transitions journal of a team in the
// period, oldest first, optionally only the given kinds.
func (u *EventAnalyticsUseCase) GetEventLabTransitions(
	ctx context.Context, eventID, teamID uuid.UUID, from, to *time.Time, kinds []string,
) (LabTransitionsView, error) {
	e, err := u.event(ctx, eventID)
	if err != nil {
		return LabTransitionsView{}, err
	}
	if !e.InfrastructureAllowed {
		return LabTransitionsView{}, nil
	}
	period, err := eventAnalyticsModel.NewPeriod(from, to, e.Lifecycle.StartAt, e.Lifecycle.EffectiveFinishAt(), u.now())
	if err != nil {
		return LabTransitionsView{}, err
	}
	rows, err := u.store.TeamLabTransitions(ctx, eventID, teamID, period, kinds, labTelemetryTransitionRows)
	if err != nil {
		return LabTransitionsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read lab transitions").Err()
	}
	return LabTransitionsView{
		Available: true, Period: PeriodView{From: period.From, To: period.To},
		Transitions: rows, Truncated: len(rows) >= labTelemetryTransitionRows,
	}, nil
}
