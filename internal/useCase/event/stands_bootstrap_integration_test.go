package event_test

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/instanceValueRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

func (f *standFixture) versionOfInfra(t *testing.T) uuid.UUID {
	t.Helper()
	var versionID uuid.UUID
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT ee.exercise_version_id FROM event_challenges c
		JOIN event_exercises ee ON ee.id = c.event_exercise_id WHERE c.id = $1`, f.infraOne).
		Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	return versionID
}

// A variant that asks for what needs a guest agent (a bootstrap script on a vm-profile device here) never reaches the agent:
// the stands fail with the reason instead of starting half-working.
func TestStandEngine_RefusesAVariantTheLaboratoryCannotApplyYet(t *testing.T) {
	f := newStandFixture(t)
	f.standWithImage(t, standPlatformImage)
	versionID := f.versionOfInfra(t)
	variant := standVariant(f.infraTaskID)
	variant.Topology.Devices = []exerciseModel.Device{
		{ID: f.infraDevice, Name: "web", Type: exerciseModel.DeviceTypeNode, Profile: exerciseModel.NodeProfileVM,
			Bootstrap: exerciseModel.Bootstrap{Script: "echo hi"}},
	}
	f.sets.variants[versionID] = variant
	f.pass(t)
	f.pass(t)
	if len(f.agent.deployed) != 0 {
		t.Fatalf("nothing may be deployed: %v", f.agent.deployed)
	}
	f.assertStatuses(t, map[string]string{"Blue": "failed", "Red": "failed"})
	view, err := f.uc.GetEventStands(context.Background(), f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range view.Items {
		if !strings.Contains(item.Reason, "device web: bootstrap.script") {
			t.Fatalf("stand %s reason %q must name what is not applied", item.TeamName, item.Reason)
		}
	}
}

// A container with a bootstrap script is deployed with it, and the variable of the infrastructure it uses is drawn once for
// the team's stand: the stand recreated under a new generation gets the same value, another team's stand its own.
func TestStandEngine_BootstrapReachesTheLabAndVariablesStayPinnedAcrossARecreate(t *testing.T) {
	f := newStandFixture(t)
	f.standWithImage(t, standPlatformImage)
	versionID := f.versionOfInfra(t)
	f.sets.topology[versionID] = exerciseModel.Topology{
		Variables: []exerciseModel.Variable{{Name: "pw", Template: "[a-z0-9]{16}"}},
		Devices: []exerciseModel.Device{{
			ID: f.infraDevice, Name: "web", Type: exerciseModel.DeviceTypeNode, Image: standPlatformImage,
			Command:   []string{"/app"},
			Bootstrap: exerciseModel.Bootstrap{Script: "echo admin:{{var.pw}} > /etc/users", Env: standTaskFlagBootstrap().Env},
		}},
	}
	variant := standVariant(f.infraTaskID)
	variant.Topology = f.sets.topology[versionID]
	f.sets.variants[versionID] = variant
	f.uc = event.NewEventUseCase(event.Dependencies{
		Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)),
		Infra: f.agent, InfrastructureCapability: standCapability{}, Topologies: f.topologies,
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now), StandPrewarmLead: 30 * time.Minute, StandDeployBudget: 200,
		StandVariables: instanceValueRepo.New(f.db.Pool),
	})
	ctx := context.Background()
	f.pass(t)

	passwordOf := func(lab string) string {
		t.Helper()
		f.agent.mu.Lock()
		defer f.agent.mu.Unlock()
		topology, ok := f.agent.topos[lab]
		if !ok {
			t.Fatalf("lab %q was not deployed: %v", lab, f.agent.deployed)
		}
		m := regexp.MustCompile(`^echo admin:([a-z0-9]{16}) > /etc/users$`).FindStringSubmatch(topology.Devices[0].Bootstrap.Script)
		if m == nil {
			t.Fatalf("script %q has no pinned value", topology.Devices[0].Bootstrap.Script)
		}
		return m[1]
	}
	if len(f.agent.deployed) < 2 {
		t.Fatalf("deployed = %v", f.agent.deployed)
	}
	pinned := func(team uuid.UUID) string {
		t.Helper()
		var value string
		if err := f.db.Pool.QueryRow(ctx, `SELECT value FROM stand_variables WHERE event_team_id = $1 AND name = 'pw'`, team).Scan(&value); err != nil {
			t.Fatalf("no pinned value of team %s: %v", team, err)
		}
		return value
	}
	blue, red := pinned(f.blueID), pinned(f.redID)
	if blue == red {
		t.Fatalf("two stands share one value %q", blue)
	}
	seen := map[string]bool{}
	for _, lab := range f.agent.deployed[:2] {
		seen[passwordOf(lab)] = true
	}
	if !seen[blue] || !seen[red] {
		t.Fatalf("the labs got %v, the stands pinned %q and %q", seen, blue, red)
	}

	// Recreate Blue: the new generation is another lab with the very same value.
	deployedBefore := len(f.agent.deployed)
	if _, err := f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	if len(f.agent.deployed) != deployedBefore+1 {
		t.Fatalf("deployed = %v", f.agent.deployed)
	}
	recreated := f.agent.deployed[len(f.agent.deployed)-1]
	if !strings.HasSuffix(recreated, "-g1") {
		t.Fatalf("recreated lab %q", recreated)
	}
	if got := passwordOf(recreated); got != blue {
		t.Fatalf("the recreated lab got %q, the stand pinned %q", got, blue)
	}
	if again := pinned(f.blueID); again != blue {
		t.Fatalf("the pinned value changed to %q", again)
	}
}
