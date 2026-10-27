package eventAnalytics

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

type labTelemetryUseCase struct {
	IUseCase
	levels      []eventAnalyticsUseCase.Level
	telemetry   eventAnalyticsUseCase.LabTelemetryView
	transitions eventAnalyticsUseCase.LabTransitionsView
	kinds       []string
	team        uuid.UUID
	people      bool
}

func (f *labTelemetryUseCase) EventAnalyticsAccess(context.Context, uuid.UUID, rbac.Claims) (eventAnalyticsModel.Access, error) {
	return eventAnalyticsModel.Access{People: f.people}, nil
}

func (f *labTelemetryUseCase) RequireEventAnalytics(_ context.Context, _ uuid.UUID, _ rbac.Claims, level eventAnalyticsUseCase.Level) error {
	f.levels = append(f.levels, level)
	return nil
}
func (f *labTelemetryUseCase) GetEventLabTelemetry(
	_ context.Context, _, team uuid.UUID, _, _ *time.Time,
) (eventAnalyticsUseCase.LabTelemetryView, error) {
	f.team = team
	return f.telemetry, nil
}
func (f *labTelemetryUseCase) GetEventLabTransitions(
	_ context.Context, _, team uuid.UUID, _, _ *time.Time, kinds []string,
) (eventAnalyticsUseCase.LabTransitionsView, error) {
	f.team, f.kinds = team, kinds
	return f.transitions, nil
}

func serveLabTelemetry(t *testing.T, uc *labTelemetryUseCase, path string) (int, []byte) {
	t.Helper()
	w := serveTasks(t, &taskUseCase{IUseCase: uc}, analyticsURL(path))
	return w.Code, w.Body.Bytes()
}

func TestLabTelemetryReport(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	at := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	cpuMax := int64(90)
	uc := &labTelemetryUseCase{people: true, telemetry: eventAnalyticsUseCase.LabTelemetryView{
		Available: true,
		Devices: []eventAnalyticsRepo.DeviceMinute{
			{LabName: "web", DeviceName: "app", BucketAt: at, Samples: 12, CPUSamples: 10, CPUSum: 500, CPUMax: &cpuMax, MemorySamples: 12, MemorySum: 1200, ReadySamples: 11, LastPodPhase: "Running"},
			{LabName: "web", DeviceName: "db", BucketAt: at, Samples: 12},
		},
		Labs:    []eventAnalyticsRepo.LabMinute{{LabName: "web", BucketAt: at, Samples: 12, ReadySamples: 12, VPNReadySamples: 11}},
		Clients: []eventAnalyticsRepo.ClientMinute{{ClientName: "p-a", BucketAt: at, Samples: 12, ConnectedSamples: 12, RxBytes: 100, TxBytes: 7}},
	}}

	code, body := serveLabTelemetry(t, uc, "lab-telemetry?team_id="+team.String())
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, team, uc.team)
	var out struct {
		Data labTelemetryResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	d := out.Data.Devices
	require.Len(t, d, 2)
	require.EqualValues(t, 50, *d[0].CPUAvg)
	require.EqualValues(t, 100, *d[0].MemoryAvg)
	require.Nil(t, d[1].CPUAvg, "no samples, no average")
	require.Equal(t, "Running", d[0].LastPodPhase)
	require.EqualValues(t, 11, out.Data.Labs[0].VPNReadySamples)
	require.EqualValues(t, 100, out.Data.Clients[0].RxBytes)
	require.Contains(t, string(body), `"CPUAvgMillicores"`)
	require.Contains(t, string(body), `"VPNReadySamples"`)

	code, _ = serveLabTelemetry(t, uc, "lab-telemetry")
	require.Equal(t, http.StatusBadRequest, code, "the team is required")
}

func TestLabTransitionsReport(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	at := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	platform := at.Add(7 * time.Second)
	uc := &labTelemetryUseCase{people: true, transitions: eventAnalyticsUseCase.LabTransitionsView{
		Available: true,
		Transitions: []eventAnalyticsRepo.LabTransition{
			{ID: uuid.Must(uuid.NewV7()), Kind: "lab_ready", LabName: "web", At: at, ReceivedAt: at.Add(700 * time.Millisecond), PlatformReadyAt: &platform},
			{ID: uuid.Must(uuid.NewV7()), Kind: "lab_not_ready", LabName: "web", At: at.Add(time.Minute), ReceivedAt: at.Add(time.Minute)},
		},
	}}

	code, body := serveLabTelemetry(t, uc, "lab-telemetry/transitions?team_id="+team.String()+"&kinds=lab_ready,lab_not_ready")
	require.Equal(t, http.StatusOK, code, string(body))
	require.Equal(t, []string{"lab_ready", "lab_not_ready"}, uc.kinds)
	var out struct {
		Data labTransitionsResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	first, second := out.Data.Transitions[0], out.Data.Transitions[1]
	require.EqualValues(t, 700, first.ReportLagMillis)
	require.EqualValues(t, 7000, *first.PlatformLagMillis)
	require.Nil(t, second.PlatformLagMillis)
	require.Nil(t, second.PlatformReadyAt)

	code, _ = serveLabTelemetry(t, uc, "lab-telemetry/transitions?team_id="+team.String()+"&kinds=nope")
	require.Equal(t, http.StatusBadRequest, code)
}

// Without the people level the VPN clients and the client transitions are left out; containers, labs and the
// stand transitions stay.
func TestLabTelemetryHidesPeopleWithoutThePeopleLevel(t *testing.T) {
	team := uuid.Must(uuid.NewV7())
	at := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	uc := &labTelemetryUseCase{
		telemetry: eventAnalyticsUseCase.LabTelemetryView{
			Available: true,
			Labs:      []eventAnalyticsRepo.LabMinute{{LabName: "web", BucketAt: at, Samples: 1}},
			Clients:   []eventAnalyticsRepo.ClientMinute{{ClientName: "p-secret", BucketAt: at, RxBytes: 5}},
		},
	}
	code, body := serveLabTelemetry(t, uc, "lab-telemetry?team_id="+team.String())
	require.Equal(t, http.StatusOK, code)
	require.NotContains(t, string(body), "p-secret")
	var out struct {
		Data labTelemetryResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(body, &out))
	require.Len(t, out.Data.Labs, 1)
	require.Empty(t, out.Data.Clients)

	code, _ = serveLabTelemetry(t, uc, "lab-telemetry/transitions?team_id="+team.String())
	require.Equal(t, http.StatusOK, code)
	require.NotEmpty(t, uc.kinds)
	for _, kind := range uc.kinds {
		require.NotContains(t, kind, "client_", "the query never asks for client transitions")
	}

	uc.kinds = nil
	code, body = serveLabTelemetry(t, uc, "lab-telemetry/transitions?team_id="+team.String()+"&kinds=client_connected")
	require.Equal(t, http.StatusOK, code)
	require.Nil(t, uc.kinds, "nothing is read when only client kinds were asked for")
	require.Contains(t, string(body), `"Transitions":[]`)

	uc.people = true
	_, _ = serveLabTelemetry(t, uc, "lab-telemetry/transitions?team_id="+team.String()+"&kinds=client_connected")
	require.Equal(t, []string{"client_connected"}, uc.kinds)
}
