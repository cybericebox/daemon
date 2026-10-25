package event_test

import (
	"context"
	"strings"
	"testing"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// With the fair reveal («all ready») the same exercise of every team forms one
// deploy group, and one engine pass submits its members back to back: all
// teams' lab of the first exercise, then all teams' lab of the second, never a
// team's whole stand at a time. The team's group is created by its first lab,
// so every group exists before the second deploy group is submitted.
func TestStandEngine_FairRevealSubmitsDeployGroupsTogether(t *testing.T) {
	f := newStandFixture(t)
	f.attach(t, true)
	f.pass(t)

	if len(f.agent.deployed) != 6 {
		t.Fatalf("deployed = %v, want 3 teams x 2 exercises", f.agent.deployed)
	}
	var tasks []string
	groups := map[string]bool{}
	for _, key := range f.agent.deployed {
		meta := f.agent.metas[key]
		task := meta.Labels[infraModel.LabelTask]
		if meta.DeployGroup != task {
			t.Fatalf("lab %s deploy group = %q, want its exercise %s", key, meta.DeployGroup, task)
		}
		tasks = append(tasks, task)
		groups[strings.SplitN(key, "/", 2)[0]] = true
	}
	if len(groups) != 3 {
		t.Fatalf("groups = %v, want 3 teams", groups)
	}
	// Exercise-major: the first three submissions share one exercise, the last three the other.
	if tasks[0] != tasks[1] || tasks[1] != tasks[2] || tasks[3] != tasks[4] || tasks[4] != tasks[5] || tasks[0] == tasks[3] {
		t.Fatalf("submission order by exercise = %v, want one deploy group after the other", tasks)
	}
}

// With «as ready» every lab is its own deploy group.
func TestStandEngine_AsReadyRevealHasNoDeployGroup(t *testing.T) {
	f := newStandFixture(t)
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE event_configs SET task_reveal_mode = 'as_ready' WHERE event_id = $1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	if len(f.agent.metas) == 0 {
		t.Fatal("nothing deployed")
	}
	for key, meta := range f.agent.metas {
		if meta.DeployGroup != "" {
			t.Fatalf("lab %s deploy group = %q, want none", key, meta.DeployGroup)
		}
	}
}
