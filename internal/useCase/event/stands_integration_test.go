package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// standAgent is an in-memory Laboratory: Labs become ready or fail on demand.
type standAgent struct {
	mu        sync.Mutex
	deployed  []string
	topos     map[string]exerciseModel.Topology
	deleted   []string
	destroyed []string
	ready     map[string]bool
	failed    map[string]bool
	// queued labs report phase Queued with this queue state.
	queued       map[string]*exerciseModel.LabQueue
	metas        map[string]infraModel.LabMeta
	deviceCalls  []string
	deviceErr    error
	prewarmCalls [][]string
	prewarmState string
	prewarmErr   error
}

func (a *standAgent) PrewarmImages(_ context.Context, images []string) ([]infraModel.ImagePrewarm, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.prewarmCalls = append(a.prewarmCalls, append([]string(nil), images...))
	if a.prewarmErr != nil {
		return nil, a.prewarmErr
	}
	state := a.prewarmState
	if state == "" {
		state = infraModel.PrewarmWarming
	}
	out := make([]infraModel.ImagePrewarm, 0, len(images))
	for _, image := range images {
		out = append(out, infraModel.ImagePrewarm{Image: image, State: state})
	}
	return out, nil
}

func newStandAgent() *standAgent {
	return &standAgent{ready: map[string]bool{}, failed: map[string]bool{}, topos: map[string]exerciseModel.Topology{}, queued: map[string]*exerciseModel.LabQueue{}, metas: map[string]infraModel.LabMeta{}}
}

func (a *standAgent) ResetDevice(_ context.Context, group, lab, device string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deviceCalls = append(a.deviceCalls, "reset "+group+"/"+lab+"/"+device)
	return a.deviceErr
}

func (a *standAgent) RescueDevice(_ context.Context, group, lab, device string, enable bool) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deviceCalls = append(a.deviceCalls, "rescue "+group+"/"+lab+"/"+device+"/"+strconv.FormatBool(enable))
	return a.deviceErr
}

func (a *standAgent) DeployLab(_ context.Context, group, lab string, meta infraModel.LabMeta, topology exerciseModel.Topology) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.metas[group+"/"+lab] = meta
	if len(topology.Devices) == 0 {
		return errors.New("static topology must never be deployed")
	}
	a.deployed = append(a.deployed, group+"/"+lab)
	a.topos[group+"/"+lab] = topology
	return nil
}

func (a *standAgent) LabStatus(_ context.Context, group, lab string) (exerciseModel.LabDeployStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch key := group + "/" + lab; {
	case a.queued[key] != nil:
		return exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseQueued, Queue: a.queued[key]}, nil
	case a.failed[key]:
		return exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseFailed}, nil
	case a.ready[key]:
		return exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseReady, Ready: true}, nil
	default:
		return exerciseModel.LabDeployStatus{Phase: exerciseModel.DeployPhaseProvisioning}, nil
	}
}

func (a *standAgent) EnsureVPNGroup(context.Context, string) error { return nil }
func (a *standAgent) EnsureLabClient(context.Context, string, string) (string, error) {
	return "[Interface]", nil
}
func (a *standAgent) DestroyLabGroup(_ context.Context, group string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.destroyed = append(a.destroyed, group)
	return nil
}
func (a *standAgent) DeleteLab(_ context.Context, group, lab string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.deleted = append(a.deleted, group+"/"+lab)
	return nil
}

func (a *standAgent) ListGroupLabs(_ context.Context, group string) ([]string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, lab := range a.deployed {
		if name, ok := strings.CutPrefix(lab, group+"/"); ok {
			out = append(out, name)
		}
	}
	return out, nil
}

func (a *standAgent) setAll(state map[string]bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, lab := range a.deployed {
		state[lab] = true
	}
}

// standTopologies serves the fixture's one device and the task linked to it.
type standTopologies struct {
	deviceID, taskID uuid.UUID
	sets             *standSets
}

// standSets holds the multi-task sets a test attached, by pinned version id.
type standSets struct {
	topology map[uuid.UUID]exerciseModel.Topology
	links    map[uuid.UUID][]exerciseModel.FlagLink
}

func (s standTopologies) ResolveDeployedTopology(_ context.Context, versionID uuid.UUID, _ int32) (exerciseModel.Topology, error) {
	if topology, ok := s.sets.topology[versionID]; ok {
		return topology, nil
	}
	return exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: s.deviceID, Name: "web"}}}, nil
}

func (s standTopologies) ResolveVersionTopologies(context.Context, uuid.UUID) ([]exerciseModel.Topology, error) {
	return []exerciseModel.Topology{{Devices: []exerciseModel.Device{{ID: s.deviceID, Name: "web", Image: "nginx:1"}, {ID: uuid.Must(uuid.NewV7()), Name: "db", Image: "postgres:16"}}}, {Devices: []exerciseModel.Device{{Name: "web", Image: "nginx:1"}}}}, nil
}

func (s standTopologies) ResolveDeployedFlagLinks(_ context.Context, versionID uuid.UUID, _ int32) ([]exerciseModel.FlagLink, error) {
	if links, ok := s.sets.links[versionID]; ok {
		return append([]exerciseModel.FlagLink(nil), links...), nil
	}
	return []exerciseModel.FlagLink{{TaskID: s.taskID, DeviceID: s.deviceID, Var: "TASK_FLAG"}}, nil
}

type standCapability struct{}

func (standCapability) RequireLaboratories(context.Context) error { return nil }

type standFixture struct {
	db                        *testhelpers.TestDB
	uc                        *event.EventUseCase
	agent                     *standAgent
	eventID                   uuid.UUID
	ownerID                   uuid.UUID
	blueID, redID, smallID    uuid.UUID
	staticChallenge, infraOne uuid.UUID
	infraTaskID, infraDevice  uuid.UUID
	sets                      *standSets
}

// journalCollector stands in for the error journal and keeps what is reported.
type journalCollector struct {
	mu     sync.Mutex
	events []errorJournal.Event
}

func (c *journalCollector) Report(e errorJournal.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *journalCollector) kind(k errorJournal.Kind) []errorJournal.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []errorJournal.Event
	for _, e := range c.events {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

func TestStandEngine_StrictAvailabilityRecreateFailureAndTeardown(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	journal := &journalCollector{}
	errorJournal.SetReporter(journal)
	t.Cleanup(func() { errorJournal.SetReporter(nil) })

	// Pass 1 (inside the deploy window, before start): every admitted team and
	// the moderators team get assignments; static challenges are published at
	// once, infrastructure Labs are deployed. The small team is not admitted.
	f.pass(t)
	moderators, err := f.db.Queries.GetModeratorsTeam(ctx, f.eventID)
	if err != nil || !moderators.Hidden || !moderators.Moderators {
		t.Fatalf("moderators team = %+v, %v", moderators, err)
	}
	if got := f.readiness(t, f.blueID, f.staticChallenge); got != 2 {
		t.Fatalf("static challenge readiness = %d, want published", got)
	}
	if got := f.readiness(t, f.blueID, f.infraOne); got != 0 {
		t.Fatalf("infrastructure challenge readiness = %d, want preparing", got)
	}
	if n := f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id = $1`, f.smallID); n != 0 {
		t.Fatalf("a not admitted team must get no assignments, got %d", n)
	}
	if len(f.agent.deployed) != 3 {
		t.Fatalf("deployed Labs = %v, want blue, red and moderators", f.agent.deployed)
	}
	f.pass(t)
	if len(f.agent.deployed) != 3 {
		t.Fatalf("a Lab must be deployed once, got %v", f.agent.deployed)
	}
	f.assertStatuses(t, map[string]string{"": "creating", "Blue": "creating", "Red": "creating"})

	// All Labs ready before the start: stands are ready but the barrier waits
	// for the start.
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	f.assertStatuses(t, map[string]string{"": "ready", "Blue": "ready", "Red": "ready"})
	if got := f.readiness(t, f.blueID, f.infraOne); got != 1 {
		t.Fatalf("before start infrastructure readiness = %d, want ready", got)
	}

	// After the start every stand is ready at once: the barrier opens.
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	f.pass(t)
	for _, team := range []uuid.UUID{f.blueID, f.redID, moderators.ID} {
		if got := f.readiness(t, team, f.infraOne); got != 2 {
			t.Fatalf("team %s infrastructure readiness = %d, want published", team, got)
		}
	}
	if view, err := f.uc.GetEventStands(ctx, f.eventID); err != nil || !view.ChallengesOpened || view.Summary.Ready != 3 || !view.Items[0].Moderators {
		t.Fatalf("stands view = %+v, %v", view, err)
	}

	// Recreate: the old Lab is deleted, the new generation deploys under a new
	// name, the published challenge stays on the board.
	recreated, err := f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, uuid.Nil)
	if err != nil || recreated.Status != eventStandModel.StatusCreating || recreated.Generation != 1 {
		t.Fatalf("recreate = %+v, %v", recreated, err)
	}
	if len(f.agent.deleted) != 1 {
		t.Fatalf("deleted Labs = %v", f.agent.deleted)
	}
	if got := f.readiness(t, f.blueID, f.infraOne); got != 2 {
		t.Fatalf("a published challenge must stay published during recreate, got %d", got)
	}
	f.pass(t)
	last := f.agent.deployed[len(f.agent.deployed)-1]
	if !strings.HasSuffix(last, "-g1") {
		t.Fatalf("recreated Lab name = %q, want a new generation", last)
	}

	// The recreated Lab fails: the stand fails and every owner/moderator gets
	// exactly one urgent signal.
	f.agent.mu.Lock()
	f.agent.failed[last] = true
	f.agent.mu.Unlock()
	f.pass(t)
	f.pass(t)
	f.assertStatuses(t, map[string]string{"Blue": "failed"})
	// A failed lab deploy goes to the error journal once, with the lab named and no team data.
	if failed := journal.kind(errorJournal.KindLabDeploy); len(failed) != 1 || !strings.HasSuffix(last, "/"+failed[0].Details["lab"]) || failed[0].Message == "" {
		t.Fatalf("lab deploy journal events = %+v, want exactly one for %s", failed, last)
	}
	if n := f.count(t, `SELECT count(*) FROM signal_outbox WHERE signal_type = 'event.lab.failed' AND payload->>'team_id' = $1`, f.blueID.String()); n != 1 {
		t.Fatalf("event.lab.failed signals = %d, want 1", n)
	}
	if _, err = f.uc.RecreateTeamStand(ctx, f.eventID, f.smallID, uuid.Nil); !errors.Is(err, eventStandModel.ErrStandTeamNotFound.Err()) {
		t.Fatalf("recreate of a team without a stand = %v", err)
	}

	// Teardown N minutes after the finish removes every team LabGroup and ends
	// the rollout.
	f.shiftLifecycle(t, -3*time.Hour, -2*time.Hour)
	f.pass(t)
	if len(f.agent.destroyed) != 4 {
		t.Fatalf("destroyed groups = %v, want every event team", f.agent.destroyed)
	}
	f.assertStatuses(t, map[string]string{"": "removed", "Blue": "removed", "Red": "removed"})
	ids, err := f.db.Queries.ListStandEvents(ctx, time.Now())
	if err != nil || len(ids) != 0 {
		t.Fatalf("a torn down event must leave the window: %v, %v", ids, err)
	}
}

func TestStandEngine_StaticOnlyEventNeedsNoLaboratory(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET infrastructure_allowed = false WHERE id = $1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)),
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now),
	})
	if err := uc.ReconcileEventStands(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.readiness(t, f.blueID, f.staticChallenge); got != 2 {
		t.Fatalf("static readiness = %d, want published", got)
	}
	if got := f.readiness(t, f.blueID, f.infraOne); got != 0 {
		t.Fatalf("an infrastructure challenge on an event without the flag must never open, got %d", got)
	}
	// W3: the moderators team exists on every event in the window for the
	// moderators board, but gets no stand, Lab or access sync without
	// infrastructure.
	moderators, err := f.db.Queries.GetModeratorsTeam(ctx, f.eventID)
	if err != nil || !moderators.Hidden {
		t.Fatalf("moderators team of an event without infrastructure: %+v, %v", moderators, err)
	}
	if got := f.readiness(t, moderators.ID, f.staticChallenge); got != 2 {
		t.Fatalf("moderators static readiness = %d, want published", got)
	}
	if n := f.count(t, `SELECT count(*) FROM event_team_stands WHERE event_id = $1`, f.eventID); n != 0 {
		t.Fatalf("stand rows without infrastructure = %d", n)
	}
	if n := f.count(t, `SELECT count(*) FROM lab_bindings WHERE event_id = $1`, f.eventID); n != 0 {
		t.Fatalf("lab bindings without infrastructure = %d", n)
	}
	if err = f.db.Queries.RequestEventLabAccessSyncsForEvent(ctx, postgres.RequestEventLabAccessSyncsForEventParams{EventID: f.eventID, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err = f.db.Queries.RequestModeratorsTeamLabAccessSync(ctx, postgres.RequestModeratorsTeamLabAccessSyncParams{EventID: f.eventID, UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if n := f.count(t, `SELECT count(*) FROM event_lab_access_syncs WHERE event_team_id = $1`, moderators.ID); n != 0 {
		t.Fatalf("access syncs of the moderators team without infrastructure = %d", n)
	}
	board, err := uc.ListModeratorsBoard(ctx, f.eventID)
	if err != nil || len(board) != 1 || board[0].EventChallengeID != f.staticChallenge || !board[0].BoardPublished || board[0].Locked || board[0].SolveCount != nil {
		t.Fatalf("moderators board = %+v, %v", board, err)
	}
	if !strings.Contains(string(board[0].Snapshot), "description") {
		t.Fatalf("moderators see the full snapshot: %s", board[0].Snapshot)
	}
	// The moderators answer as a normal team: the attempts and the solve are
	// recorded, the team stays hidden, so no participant sees it anywhere.
	submit := func(challengeID uuid.UUID, answer string) (event.SubmitChallengeResult, error) {
		return uc.SubmitModeratorsChallenge(ctx, f.eventID, f.ownerID, challengeID, event.SubmitChallengeInput{Answer: answer, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: time.Now()})
	}
	for answer, want := range map[string]bool{"ICE{y}": false, " ICE{x} ": true} {
		result, submitErr := submit(f.staticChallenge, answer)
		if submitErr != nil || result.Correct != want || result.FirstSolve != want {
			t.Fatalf("submit %q = %+v, %v", answer, result, submitErr)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM challenge_attempts WHERE event_id = $1 AND event_team_id = $2`, f.eventID, moderators.ID); n != 2 {
		t.Fatalf("moderators attempts = %d, want 2", n)
	}
	if n := f.count(t, `SELECT count(*) FROM team_challenge_solves s JOIN team_challenges tc ON tc.id = s.team_challenge_id WHERE tc.event_team_id = $1`, moderators.ID); n != 1 {
		t.Fatalf("moderators solves = %d, want 1", n)
	}
	public, listErr := f.db.Queries.ListEventScoreboard(ctx, postgres.ListEventScoreboardParams{EventID: f.eventID})
	if listErr != nil {
		t.Fatal(listErr)
	}
	for _, row := range public {
		if row.TeamID == moderators.ID {
			t.Fatalf("the moderators team is on the public scoreboard: %+v", public)
		}
	}
	if n := f.count(t, `SELECT count(*) FROM event_result_changes WHERE event_id = $1`, f.eventID); n != 0 {
		t.Fatalf("a hidden team's solve must not be announced to live clients, changes = %d", n)
	}
	solvers, err := uc.ListModeratorsChallengeSolves(ctx, f.eventID, f.staticChallenge, uuid.Nil, 30)
	if err != nil || len(solvers.Items) != 1 || !solvers.Items[0].Own || solvers.Items[0].FirstBlood {
		t.Fatalf("moderators solvers = %+v, %v", solvers, err)
	}
	// «Моя участь» of the organizers' preview reads the real recorded activity.
	stats, err := uc.GetModeratorsParticipationStats(ctx, f.eventID, f.ownerID)
	if err != nil || stats.Team.TeamID != moderators.ID || stats.Team.TeamName != "" || stats.Rank != 0 || len(stats.Team.Solves) != 1 || stats.Solved != 1 ||
		stats.Team.Attempts != 2 || stats.Team.CorrectAttempts != 1 || stats.Me.UserID != f.ownerID || stats.Me.Attempts != 2 || stats.Me.Solves != 1 || stats.Me.Points != 100 || len(stats.Team.Members) == 0 {
		t.Fatalf("moderators participation = %+v, %v", stats, err)
	}
	if _, err = uc.GetModeratorsParticipationStats(ctx, f.eventID, uuid.Must(uuid.NewV7())); err == nil {
		t.Fatal("only an event manager reads the moderators team results")
	}
	if _, err = submit(f.infraOne, "ICE{x}"); !errors.Is(err, teamChallengeModel.ErrTeamChallengeTransition.Err()) {
		t.Fatalf("a preparing assignment is not on the moderators board: %v", err)
	}
	if _, err := uc.GetEventStands(ctx, f.eventID); !errors.Is(err, eventStandModel.ErrStandInfrastructureNotAllowed.Err()) {
		t.Fatalf("stands of an event without infrastructure = %v", err)
	}
}

// Every Lab of the stand gets its own team's stored flag in the linked
// device's env var, never a freshly rolled value.
func TestStandEngine_InjectsTheTeamsStoredFlagIntoTheLinkedDevice(t *testing.T) {
	f := newStandFixture(t)
	f.pass(t)
	rows, err := f.db.Pool.Query(context.Background(), `SELECT lb.lab_group_name || '/' || lb.lab_name, tc.expected_flag
		FROM lab_bindings lb JOIN team_challenges tc ON tc.event_team_id = lb.event_team_id AND tc.event_challenge_id = lb.event_challenge_id
		WHERE lb.event_id = $1`, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	checked := 0
	for rows.Next() {
		var lab, stored string
		if err = rows.Scan(&lab, &stored); err != nil {
			t.Fatal(err)
		}
		f.agent.mu.Lock()
		topology := f.agent.topos[lab]
		f.agent.mu.Unlock()
		var got []string
		for _, env := range topology.Devices[0].EnvVars {
			if env.Name == "TASK_FLAG" {
				got = append(got, env.Value)
			}
		}
		if len(got) != 1 || got[0] != stored || stored == "" {
			t.Fatalf("lab %s env TASK_FLAG = %v, want the stored %q once", lab, got, stored)
		}
		checked++
	}
	if checked != 3 {
		t.Fatalf("checked %d labs, want blue, red and moderators", checked)
	}
}

func newStandFixture(t *testing.T) *standFixture {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now()
	f := &standFixture{db: db, agent: newStandAgent(), sets: &standSets{topology: map[uuid.UUID]exerciseModel.Topology{}, links: map[uuid.UUID][]exerciseModel.FlagLink{}}}
	users := userRepo.New(db.Queries)
	newUser := func(email string) uuid.UUID {
		u, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, now))
		if err != nil {
			t.Fatal(err)
		}
		return u.ID
	}
	f.ownerID = newUser("owner@stands.test")
	e, err := eventModel.NewEvent("stands", "Stands", now.Add(-24*time.Hour), time.Time{}, f.ownerID, now)
	if err != nil {
		t.Fatal(err)
	}
	e.AllowInfrastructure(true)
	created, err := eventRepo.New(db.Queries).Create(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	f.eventID = created.ID
	config := eventConfigModel.NewEventConfig(f.eventID, now)
	team := eventConfigModel.ParticipationTeam
	config.Participation = &team
	minSize := int32(2)
	config.MinTeamSize = &minSize
	if _, err = eventConfigRepo.New(db.Queries).Create(ctx, config); err != nil {
		t.Fatal(err)
	}
	owner, err := eventManagerModel.New(f.eventID, f.ownerID, eventManagerModel.RoleOwner, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventManagerRepo.New(db.Queries).Create(ctx, owner); err != nil {
		t.Fatal(err)
	}
	f.shiftLifecycle(t, 10*time.Minute, 2*time.Hour)
	teams := eventTeamRepo.New(db.Queries)
	newTeam := func(name string, admitted bool) uuid.UUID {
		value, err := eventTeamModel.New(f.eventID, newUser(strings.ToLower(name)+"@stands.test"), name, "stand-code-"+name, now)
		if err != nil {
			t.Fatal(err)
		}
		value.AdmittedManually = admitted
		value.FormedAt = &now
		stored, err := teams.Create(ctx, value)
		if err != nil {
			t.Fatal(err)
		}
		return stored.ID
	}
	f.blueID, f.redID, f.smallID = newTeam("Blue", true), newTeam("Red", true), newTeam("Small", false)
	f.staticChallenge = f.attach(t, false)
	f.infraOne = f.attach(t, true)
	f.uc = event.NewEventUseCase(event.Dependencies{
		Repo: db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(db.Pool)),
		Infra: f.agent, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets},
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now),
		StandPrewarmLead: 30 * time.Minute, StandDeployBudget: 200,
	})
	return f
}

// attach publishes one single-task exercise (static or with a lab topology)
// and places its challenge on the event board.
func (f *standFixture) attach(t *testing.T, infrastructure bool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	exercises := exerciseRepo.New(f.db.Queries)
	name := "Static stand task"
	if infrastructure {
		name = "Infrastructure stand task"
	}
	value, err := exerciseModel.NewExercise(name+" "+uuid.Must(uuid.NewV4()).String()[:8], "desc", []string{"web"}, uuid.Nil, now)
	if err != nil {
		t.Fatal(err)
	}
	value, err = exercises.Create(ctx, value)
	if err != nil {
		t.Fatal(err)
	}
	variant := exerciseModel.Variant{Index: 0, Tasks: []exerciseModel.Task{{
		ID: uuid.Must(uuid.NewV7()), Name: "Find the flag", Description: json.RawMessage(`{"blocks":[]}`),
		Difficulty: exerciseModel.DifficultyEasy, Flag: []string{"ICE{x}"},
	}}}
	variant.Topology.Devices = []exerciseModel.Device{} // stored as [], never null, like a real exercise
	if infrastructure {
		variant.Topology.Devices = []exerciseModel.Device{{ID: uuid.Must(uuid.NewV7()), Name: "web"}}
		f.infraTaskID, f.infraDevice = variant.Tasks[0].ID, variant.Topology.Devices[0].ID
	}
	draft, err := exercises.UpsertDraft(ctx, value.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}, now, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exercises.Publish(ctx, value.ID, now); err != nil {
		t.Fatal(err)
	}
	attachment, err := f.db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
		ID: uuid.Must(uuid.NewV7()), EventID: f.eventID, ExerciseID: value.ID, ExerciseVersionID: draft.ID,
		VariantMode: 1, FixedVariantIndex: pgtype.Int4{Int32: 0, Valid: true}, Revision: 1, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	challenge, err := f.db.Queries.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{
		ID: uuid.Must(uuid.NewV7()), EventExerciseID: attachment.ID, TaskID: variant.Tasks[0].ID,
		Points: 100, Published: true, Snapshot: []byte(`{"name":"Stand task"}`), CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return challenge.ID
}

// shiftLifecycle moves start and finish relative to now (publish stays before).
func (f *standFixture) shiftLifecycle(t *testing.T, start, finish time.Duration) {
	t.Helper()
	now := time.Now()
	if _, err := f.db.Pool.Exec(context.Background(), `UPDATE events
		SET lifecycle_configured = true, publish_at = $2, start_at = $3, finish_at = $4, withdraw_at = $5
		WHERE id = $1`, f.eventID, now.Add(-48*time.Hour), now.Add(start), now.Add(finish), now.Add(finish+24*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func (f *standFixture) pass(t *testing.T) {
	t.Helper()
	if err := f.uc.ReconcileEventStands(context.Background()); err != nil {
		t.Fatalf("ReconcileEventStands: %v", err)
	}
}

func (f *standFixture) readiness(t *testing.T, teamID, challengeID uuid.UUID) int16 {
	t.Helper()
	var readiness int16
	err := f.db.Pool.QueryRow(context.Background(), `SELECT readiness FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, teamID, challengeID).Scan(&readiness)
	if err != nil {
		t.Fatalf("read readiness: %v", err)
	}
	return readiness
}

func (f *standFixture) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	if err := f.db.Pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// assertStatuses checks stand statuses by team name ("" = moderators team).
func (f *standFixture) assertStatuses(t *testing.T, want map[string]string) {
	t.Helper()
	view, err := f.uc.GetEventStands(context.Background(), f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, item := range view.Items {
		got[item.TeamName] = item.Status.String()
	}
	for name, status := range want {
		if got[name] != status {
			t.Fatalf("stand statuses = %v, want %s=%s", got, name, status)
		}
	}
}

// A queued Lab waits for the launch pacing instead of timing out, the stand detail shows its
// queue and devices, and the organizer actions reach the agent for the right Lab.
func TestStandEngine_QueuedLabsLaunchClassDetailAndDeviceActions(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)

	if len(f.agent.metas) != 3 {
		t.Fatalf("deployed lab metas = %v", f.agent.metas)
	}
	tasks := map[string]bool{}
	for lab, meta := range f.agent.metas {
		for _, key := range []string{infraModel.LabelKind, infraModel.LabelEvent, infraModel.LabelTeam, infraModel.LabelTask, infraModel.LabelVersion} {
			if meta.Labels[key] == "" {
				t.Fatalf("lab %s lacks label %s: %+v", lab, key, meta)
			}
		}
		if meta.Labels[infraModel.LabelEvent] != f.eventID.String() || meta.Labels[infraModel.LabelKind] != infraModel.KindStand || meta.GroupLabels[infraModel.LabelTeam] != meta.Labels[infraModel.LabelTeam] {
			t.Fatalf("lab %s meta = %+v", lab, meta)
		}
		// The fixture locks the rosters at the start: the task is the deploy group of every team's lab.
		if meta.DeployGroup != meta.Labels[infraModel.LabelTask] {
			t.Fatalf("lab %s deploy group = %q, want its task", lab, meta.DeployGroup)
		}
		tasks[meta.DeployGroup] = true
	}
	if len(tasks) != 1 {
		t.Fatalf("the same task of every team must share one deploy group, got %v", tasks)
	}
	// A rolling event has no common start: its labs are independent.
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET join_policy = 1 WHERE id = $1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET deployed_at = NULL, readiness = 0 WHERE event_team_id = $1`, f.redID); err != nil {
		t.Fatal(err)
	}
	f.agent.mu.Lock()
	f.agent.metas = map[string]infraModel.LabMeta{}
	f.agent.mu.Unlock()
	f.pass(t)
	for lab, meta := range f.agent.metas {
		if meta.DeployGroup != "" {
			t.Fatalf("rolling event lab %s must be independent, got %q", lab, meta.DeployGroup)
		}
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET join_policy = 0 WHERE id = $1`, f.eventID); err != nil {
		t.Fatal(err)
	}

	// Queued for far longer than the deploy timeout: still creating, never failed.
	var blueLab, blueGroup string
	if err := f.db.Pool.QueryRow(ctx, `SELECT lab_group_name, lab_name FROM lab_bindings WHERE event_team_id = $1`, f.blueID).Scan(&blueGroup, &blueLab); err != nil {
		t.Fatal(err)
	}
	f.agent.mu.Lock()
	f.agent.queued[blueGroup+"/"+blueLab] = &exerciseModel.LabQueue{Position: 2, Length: 3, Reason: exerciseModel.QueueReasonInFlightLimit, Pods: 1, Pending: 1}
	f.agent.mu.Unlock()
	if _, err := f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET deployed_at = now() - interval '3 hours' WHERE event_team_id = $1`, f.blueID); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	f.assertStatuses(t, map[string]string{"Blue": "creating"})

	detail, err := f.uc.GetTeamStandDetail(ctx, f.eventID, f.blueID)
	if err != nil || detail.TeamName != "Blue" || len(detail.Labs) != 1 || detail.Labs[0].Live == nil || detail.Labs[0].Live.Queue == nil || detail.Labs[0].Live.Queue.Position != 2 {
		t.Fatalf("detail = %+v, %v", detail, err)
	}
	if _, err = f.uc.GetTeamStandDetail(ctx, f.eventID, f.smallID); !errors.Is(err, eventStandModel.ErrStandTeamNotFound.Err()) {
		t.Fatalf("detail of a team without a stand = %v", err)
	}

	challengeID := detail.Labs[0].ChallengeID
	if err = f.uc.ResetStandDevice(ctx, f.eventID, f.blueID, challengeID, "web"); err != nil {
		t.Fatal(err)
	}
	if err = f.uc.RescueStandDevice(ctx, f.eventID, f.blueID, challengeID, "web", true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"reset " + blueGroup + "/" + blueLab + "/web", "rescue " + blueGroup + "/" + blueLab + "/web/true"}; strings.Join(f.agent.deviceCalls, "|") != strings.Join(want, "|") {
		t.Fatalf("device calls = %v, want %v", f.agent.deviceCalls, want)
	}
	if err = f.uc.ResetStandDevice(ctx, f.eventID, f.redID, challengeID, "web"); err != nil {
		t.Fatalf("red has the same challenge: %v", err)
	}
	if err = f.uc.ResetStandDevice(ctx, f.eventID, f.smallID, challengeID, "web"); !errors.Is(err, eventStandModel.ErrStandTeamNotFound.Err()) {
		t.Fatalf("reset for a team without a stand = %v", err)
	}
	if err = f.uc.ResetStandDevice(ctx, f.eventID, f.blueID, uuid.Must(uuid.NewV7()), "web"); !errors.Is(err, eventStandModel.ErrStandLabNotFound.Err()) {
		t.Fatalf("reset of an unknown challenge = %v", err)
	}
}

// The images of an event are prewarmed in the image cache from the prewarm lead before the deploy
// time until the start; the engine reports the state and a failure never stops the stand deploy.
func TestStandEngine_PrewarmsTheEventImagesBeforeTheDeployWindow(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()

	// The deploy lead is computed: a small workload gives the 10 minute floor plus the 5 minute image pre-pull, so
	// the stands deploy 15 minutes before the start and the prewarm window (30 minutes before that) opens 45
	// minutes before the start. Start in 70 minutes: it opens in 25 minutes.
	f.shiftLifecycle(t, 70*time.Minute, 3*time.Hour)
	f.pass(t)
	if len(f.agent.prewarmCalls) != 0 {
		t.Fatalf("prewarm too early: %v", f.agent.prewarmCalls)
	}

	f.shiftLifecycle(t, 40*time.Minute, 3*time.Hour)
	f.pass(t)
	if len(f.agent.prewarmCalls) != 1 || strings.Join(f.agent.prewarmCalls[0], ",") != "nginx:1,postgres:16" {
		t.Fatalf("prewarm calls = %v, want the sorted distinct images of every variant once", f.agent.prewarmCalls)
	}
	if len(f.agent.deployed) != 0 {
		t.Fatalf("stands must not deploy before their window: %v", f.agent.deployed)
	}
	view, err := f.uc.GetEventStands(ctx, f.eventID)
	if err != nil || view.Prewarm == nil || view.Prewarm.Total != 2 || view.Prewarm.Warming != 2 || view.Prewarm.Settled() {
		t.Fatalf("prewarm summary = %+v, %v", view.Prewarm, err)
	}

	// Every pass repeats the call, which polls the agent; done images settle the summary.
	f.agent.mu.Lock()
	f.agent.prewarmState = infraModel.PrewarmDone
	f.agent.mu.Unlock()
	f.pass(t)
	if view, err = f.uc.GetEventStands(ctx, f.eventID); err != nil || view.Prewarm.Done != 2 || !view.Prewarm.Settled() {
		t.Fatalf("settled summary = %+v, %v", view.Prewarm, err)
	}

	// A failing cache pauses the prewarm and never breaks the engine pass.
	f.agent.mu.Lock()
	f.agent.prewarmErr = errors.New("cache is off")
	calls := len(f.agent.prewarmCalls)
	f.agent.mu.Unlock()
	f.pass(t)
	f.pass(t)
	if len(f.agent.prewarmCalls) != calls+1 {
		t.Fatalf("a failed prewarm must back off, calls %d -> %d", calls, len(f.agent.prewarmCalls))
	}

	// From the start nothing is prewarmed any more.
	f.shiftLifecycle(t, -time.Minute, 3*time.Hour)
	f.agent.mu.Lock()
	f.agent.prewarmErr = nil
	calls = len(f.agent.prewarmCalls)
	f.agent.mu.Unlock()
	f.uc = event.NewEventUseCase(event.Dependencies{
		Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)),
		Infra: f.agent, InfrastructureCapability: standCapability{}, Topologies: standTopologies{deviceID: f.infraDevice, taskID: f.infraTaskID, sets: f.sets},
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now), StandPrewarmLead: 30 * time.Minute,
	})
	f.pass(t)
	if len(f.agent.prewarmCalls) != calls {
		t.Fatalf("a started event must not be prewarmed: %v", f.agent.prewarmCalls[calls:])
	}
}

// The organizers' stand list carries the launch queue and image warning of every stand from the
// current monitoring state, with no agent calls.
func TestGetEventStands_ShowsTheQueueAndImageWarningFromMonitoring(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	group, err := labBindingModel.GroupName(f.eventID, f.blueID)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err = f.db.Queries.UpsertLabMonitoringCurrent(ctx, postgres.UpsertLabMonitoringCurrentParams{
		EventID: f.eventID, EventTeamID: f.blueID, LabGroupName: group, AgentID: "a", Sequence: 1, ObservedAt: now, UpdatedAt: now,
		Payload: []byte(`{"groups":[{"status":{"imageWarning":"vpn:latest"}}],"labs":[
			{"status":{"phase":"Queued","scheduling":{"position":4,"length":"9","reason":"WaitingForGroup"}}},
			{"status":{"phase":"Queued","scheduling":{"position":2,"length":9,"reason":"InFlightLimit"}}}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	view, err := f.uc.GetEventStands(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	var blue, red *event.StandTeamView
	for i := range view.Items {
		switch view.Items[i].TeamID {
		case f.blueID:
			blue = &view.Items[i]
		case f.redID:
			red = &view.Items[i]
		}
	}
	if blue == nil || red == nil {
		t.Fatalf("stands = %+v", view.Items)
	}
	if blue.Launch.QueuedLabs != 2 || blue.Launch.Position != 2 || blue.Launch.Length != 9 || blue.Launch.Reason != "InFlightLimit" || !blue.Launch.ImageWarning {
		t.Fatalf("blue launch = %+v", blue.Launch)
	}
	if red.Launch.QueuedLabs != 0 || red.Launch.ImageWarning {
		t.Fatalf("a stand without observations has no queue: %+v", red.Launch)
	}
}

// The task reveal mode is the organizer's choice until the event starts: it is stored, shown in the
// config view and locked after the start (an unchanged value is still accepted).
func TestUpdateEventConfig_TaskRevealModeIsLockedAfterTheStart(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	asReady := eventConfigModel.RevealAsReady
	allReady := eventConfigModel.RevealAllReady
	update := func(mode *eventConfigModel.TaskRevealMode) (event.EventConfigView, error) {
		view, err := f.uc.GetEventConfig(ctx, f.eventID)
		if err != nil {
			t.Fatal(err)
		}
		return f.uc.UpdateEventConfig(ctx, f.eventID, event.UpdateConfigInput{
			Registration: view.Registration, ScoreboardVisibility: view.ScoreboardVisibility, ParticipantsVisibility: view.ParticipantsVisibility,
			MaxTeamSize: view.MaxTeamSize, MinTeamSize: view.MinTeamSize, MaxTeams: view.MaxTeams, TaskRevealMode: mode,
		}, f.ownerID)
	}
	if view, err := f.uc.GetEventConfig(ctx, f.eventID); err != nil || view.TaskRevealMode != eventConfigModel.RevealAllReady {
		t.Fatalf("default = %+v, %v", view.TaskRevealMode, err)
	}
	// Before the start (the fixture starts in 10 minutes).
	view, err := update(&asReady)
	if err != nil || view.TaskRevealMode != eventConfigModel.RevealAsReady {
		t.Fatalf("before the start: %+v, %v", view.TaskRevealMode, err)
	}
	if again, getErr := f.uc.GetEventConfig(ctx, f.eventID); getErr != nil || again.TaskRevealMode != eventConfigModel.RevealAsReady {
		t.Fatalf("not persisted: %+v, %v", again.TaskRevealMode, getErr)
	}
	// Omitted keeps the mode; an unknown value is refused.
	if view, err = update(nil); err != nil || view.TaskRevealMode != eventConfigModel.RevealAsReady {
		t.Fatalf("omitted: %+v, %v", view.TaskRevealMode, err)
	}
	bad := eventConfigModel.TaskRevealMode("gradual")
	if _, err = update(&bad); !errors.Is(err, eventConfigModel.ErrTaskRevealModeInvalid.Err()) {
		t.Fatalf("unknown value: %v", err)
	}
	// After the start.
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	if _, err = update(&allReady); !errors.Is(err, eventConfigModel.ErrTaskRevealModeLocked.Err()) {
		t.Fatalf("after the start: %v, want locked", err)
	}
	if view, err = update(&asReady); err != nil || view.TaskRevealMode != eventConfigModel.RevealAsReady {
		t.Fatalf("an unchanged value after the start: %+v, %v", view.TaskRevealMode, err)
	}
}
