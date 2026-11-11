package event

import (
	"context"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// memoryStandVariables is the store of the pinned variables in memory, keyed like the table.
type memoryStandVariables struct {
	mu     sync.Mutex
	values map[[2]uuid.UUID]map[string]string
	pins   int
}

func (m *memoryStandVariables) Stand(_ context.Context, team, exercise uuid.UUID) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]string{}
	for name, value := range m.values[[2]uuid.UUID{team, exercise}] {
		out[name] = value
	}
	return out, nil
}

func (m *memoryStandVariables) PinStand(_ context.Context, team, exercise uuid.UUID, drawn map[string]string, _ time.Time) (map[string]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pins++
	key := [2]uuid.UUID{team, exercise}
	if m.values == nil {
		m.values = map[[2]uuid.UUID]map[string]string{}
	}
	if m.values[key] == nil {
		m.values[key] = map[string]string{}
	}
	for name, value := range drawn {
		if _, taken := m.values[key][name]; !taken {
			m.values[key][name] = value
		}
	}
	out := map[string]string{}
	for name, value := range m.values[key] {
		out[name] = value
	}
	return out, nil
}

type variantOnlyResolver struct{ variant exerciseModel.Variant }

func (variantOnlyResolver) ResolveDeployedTopology(context.Context, uuid.UUID, int32) (exerciseModel.Topology, error) {
	return exerciseModel.Topology{}, nil
}

func (r variantOnlyResolver) ResolveDeployedVariant(context.Context, uuid.UUID, int32) (exerciseModel.Variant, error) {
	return r.variant, nil
}

func variantWithPinnedVariable() (exerciseModel.Variant, exerciseModel.Topology) {
	web := uuid.Must(uuid.NewV7())
	device := exerciseModel.Device{
		ID: web, Name: "web", Type: exerciseModel.DeviceTypeNode,
		Bootstrap: exerciseModel.Bootstrap{
			Script: "echo admin:{{var.pw}} > /etc/users",
			Files:  []exerciseModel.BootstrapFile{{Path: "/etc/app.conf", Content: "mode={{var.mode}}"}},
		},
	}
	state := exerciseModel.Task{
		Key: "configured",
		Check: exerciseModel.Check{State: &exerciseModel.StateCheck{Conditions: []exerciseModel.Condition{
			{DeviceID: web, File: &exerciseModel.FileObservation{Path: "/etc/users", Contains: "admin:{{var.pw}}"}},
		}}},
	}
	topology := exerciseModel.Topology{
		Variables: []exerciseModel.Variable{{Name: "pw", Template: "[a-z0-9]{16}"}, {Name: "mode", Value: "ctf"}},
		Devices:   []exerciseModel.Device{device},
	}
	return exerciseModel.Variant{Tasks: []exerciseModel.Task{state}, Topology: topology}, topology
}

// The variables of the infrastructure are drawn once per team's stand: a rebuilt lab (the same call again) gets the same
// values in its bootstrap and in the expectations of its checks, another team's stand gets its own.
func TestWithTeamBootstraps_PinsVariablesAcrossBuilds(t *testing.T) {
	variant, topology := variantWithPinnedVariable()
	store := &memoryStandVariables{}
	u := &EventUseCase{topologies: variantOnlyResolver{variant: variant}, standVariables: store}
	lab := func(team uuid.UUID) labBindingRepo.PendingLab {
		return labBindingRepo.PendingLab{
			Binding:           labBindingModel.Binding{EventTeamID: team},
			ExerciseVersionID: uuid.Must(uuid.NewV7()),
			EventExerciseID:   uuid.FromStringOrNil("00000000-0000-7000-8000-0000000000aa"),
		}
	}
	teamA, teamB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	passwordOf := func(team uuid.UUID) (string, []exerciseModel.LabCheck) {
		built, checks, err := u.withTeamBootstraps(context.Background(), lab(team), topology)
		require.NoError(t, err)
		m := regexp.MustCompile(`^echo admin:([a-z0-9]{16}) > /etc/users$`).FindStringSubmatch(built.Devices[0].Bootstrap.Script)
		require.NotNil(t, m, built.Devices[0].Bootstrap.Script)
		require.Equal(t, "mode=ctf", built.Devices[0].Bootstrap.Files[0].Content)
		require.Len(t, checks, 1)
		require.Equal(t, "configured:1", checks[0].ID)
		require.Equal(t, "admin:"+m[1], checks[0].Parts[0].File.Contains, "the expectation holds the same value")
		return m[1], checks
	}

	first, _ := passwordOf(teamA)
	second, _ := passwordOf(teamA) // the lab is rebuilt or reset
	third, _ := passwordOf(teamA)
	require.Equal(t, first, second)
	require.Equal(t, first, third)
	require.Equal(t, 1, store.pins, "drawn and stored once")
	other, _ := passwordOf(teamB)
	require.NotEqual(t, first, other, "every team's stand has its own value")
	require.Equal(t, 2, store.pins)

	// The stored topology is not touched by a build.
	require.Contains(t, topology.Devices[0].Bootstrap.Script, "{{var.pw}}")
}
