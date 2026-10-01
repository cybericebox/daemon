package lab

import (
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

type scriptedStream struct {
	updates []*labpb.MonitoringUpdate
	next    int
}

func (s *scriptedStream) Recv() (*labpb.MonitoringUpdate, error) {
	if s.next >= len(s.updates) {
		return nil, io.EOF
	}
	update := s.updates[s.next]
	s.next++
	return update, nil
}

type appliedCurrent struct {
	group    string
	agent    string
	sequence int64
	snapshot bool
	payload  string
}

type recordingStore struct {
	appended   []labMonitoringModel.Observation
	capacities []labMonitoringModel.CapacityObservation
	applied    []appliedCurrent
	pruned     []string
}

func (s *recordingStore) ApplyCurrent(_ context.Context, group, agent string, sequence int64, _, _ time.Time, snapshot bool, payload json.RawMessage) error {
	s.applied = append(s.applied, appliedCurrent{group: group, agent: agent, sequence: sequence, snapshot: snapshot, payload: string(payload)})
	return nil
}
func (s *recordingStore) PruneCurrent(_ context.Context, agent string, _ time.Time) error {
	s.pruned = append(s.pruned, agent)
	return nil
}

func (s *recordingStore) ActiveTeams(context.Context, string, time.Time) ([]labMonitoringModel.EventTeam, error) {
	return []labMonitoringModel.EventTeam{{EventID: uuid.Must(uuid.NewV7()), EventTeamID: uuid.Must(uuid.NewV7())}}, nil
}
func (s *recordingStore) Append(_ context.Context, observation labMonitoringModel.Observation) (labMonitoringModel.Observation, error) {
	s.appended = append(s.appended, observation)
	return observation, nil
}
func (s *recordingStore) AppendCapacity(_ context.Context, observation labMonitoringModel.CapacityObservation) (labMonitoringModel.CapacityObservation, error) {
	s.capacities = append(s.capacities, observation)
	return observation, nil
}

func TestConsumePersistsClusterCapacityWithoutAnEventTeam(t *testing.T) {
	store := &recordingStore{}
	stream := &scriptedStream{updates: []*labpb.MonitoringUpdate{{
		AgentId: "agent-a", Sequence: 1, ObservedAtUnixMs: time.Now().UnixMilli(), SchemaVersion: 1, Snapshot: true,
		Capacity: &labpb.CapacityResponse{Tenant: "platform", HasCpuQuota: true, CpuQuotaMillicores: 4000, CpuReservedMillicores: 1500},
	}}}
	runner := NewRunner(nil, func(context.Context, *labpb.MonitoringRequest) (Stream, error) { return stream, nil }, store)
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("consume error = %v, want EOF after the scripted stream", err)
	}
	if len(store.capacities) != 1 || string(store.capacities[0].Payload) == "" {
		t.Fatalf("capacity observations = %#v, want one durable record", store.capacities)
	}
}

func update(epoch string, sequence int64, snapshot bool, groups ...string) *labpb.MonitoringUpdate {
	u := &labpb.MonitoringUpdate{AgentId: "agent-a", AgentEpoch: epoch, Sequence: sequence, ObservedAtUnixMs: time.Now().UnixMilli(), SchemaVersion: 1, Snapshot: snapshot}
	for _, g := range groups {
		u.Groups = append(u.Groups, &labpb.LabGroup{Name: g})
	}
	return u
}

func openOnce(stream Stream, requests *[]*labpb.MonitoringRequest) OpenStream {
	return func(_ context.Context, request *labpb.MonitoringRequest) (Stream, error) {
		*requests = append(*requests, request)
		return stream, nil
	}
}

func TestConsumeAcceptsSkippedSequencesBecauseTheSelectorFiltersUpdates(t *testing.T) {
	store := &recordingStore{}
	stream := &scriptedStream{updates: []*labpb.MonitoringUpdate{update("e1", 1, true, "group-a"), update("e1", 9, false, "group-a")}}
	var requests []*labpb.MonitoringRequest
	runner := NewRunner(nil, openOnce(stream, &requests), store).WithSelector("cybericebox.io/instance=prod")
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("consume error = %v, want EOF after both updates", err)
	}
	if len(store.appended) != 2 {
		t.Fatalf("observations = %d, want the snapshot and the delta after a gap in the numbers", len(store.appended))
	}
	if requests[0].GetSelector() != "cybericebox.io/instance=prod" || requests[0].GetAgentEpoch() != "" || requests[0].GetResumeAfterSequence() != 0 {
		t.Fatalf("first request = %+v, want the selector and no resume", requests[0])
	}
}

func TestConsumeResumesAfterTheLastProcessedPosition(t *testing.T) {
	store := &recordingStore{}
	var requests []*labpb.MonitoringRequest
	runner := NewRunner(nil, openOnce(&scriptedStream{updates: []*labpb.MonitoringUpdate{update("e1", 1, true, "group-a"), update("e1", 7, false, "group-a")}}, &requests), store)
	_ = runner.consume(context.Background())
	// The reconnect asks to resume; the agent replays the missed updates without a snapshot.
	runner.open = openOnce(&scriptedStream{updates: []*labpb.MonitoringUpdate{update("e1", 12, false, "group-a")}}, &requests)
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("resumed consume error = %v, want EOF", err)
	}
	if got := requests[1]; got.GetAgentEpoch() != "e1" || got.GetResumeAfterSequence() != 7 {
		t.Fatalf("resume request = %+v, want epoch e1 after sequence 7", got)
	}
	if len(store.applied) != 3 || store.applied[2].snapshot || store.applied[2].sequence != 12 {
		t.Fatalf("applied = %+v, want the replayed delta applied on top", store.applied)
	}
	// The agent could not resume and sends a full snapshot of a new epoch: it replaces what we hold.
	runner.open = openOnce(&scriptedStream{updates: []*labpb.MonitoringUpdate{update("e2", 3, true, "group-b")}}, &requests)
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("snapshot after resume error = %v", err)
	}
	if runner.position != (position{epoch: "e2", sequence: 3}) || len(store.pruned) != 2 {
		t.Fatalf("position = %+v pruned = %v", runner.position, store.pruned)
	}
}

func TestConsumeRequiresASnapshotWhenTheEpochChangesOrNothingIsHeld(t *testing.T) {
	var requests []*labpb.MonitoringRequest
	fresh := NewRunner(nil, openOnce(&scriptedStream{updates: []*labpb.MonitoringUpdate{update("e1", 4, false, "group-a")}}, &requests), &recordingStore{})
	if err := fresh.consume(context.Background()); err == nil || err == io.EOF {
		t.Fatalf("a first delta without a snapshot must be refused, err=%v", err)
	}
	store := &recordingStore{}
	runner := NewRunner(nil, openOnce(&scriptedStream{updates: []*labpb.MonitoringUpdate{update("e1", 1, true, "group-a"), update("e2", 2, false, "group-a")}}, &requests), store)
	err := runner.consume(context.Background())
	if err == nil || err == io.EOF || len(store.appended) != 1 {
		t.Fatalf("a delta of another epoch must stop the stream, err=%v observations=%d", err, len(store.appended))
	}
	if runner.position != (position{}) {
		t.Fatalf("position = %+v, the next connect must ask for a fresh snapshot", runner.position)
	}
}

func TestConsumePersistsDeletionOnlyMonitoringUpdate(t *testing.T) {
	store := &recordingStore{}
	stream := &scriptedStream{updates: []*labpb.MonitoringUpdate{
		{AgentId: "agent-a", Sequence: 1, ObservedAtUnixMs: time.Now().UnixMilli(), SchemaVersion: 1, Snapshot: true, Groups: []*labpb.LabGroup{{Name: "group-a"}}},
		{AgentId: "agent-a", Sequence: 2, ObservedAtUnixMs: time.Now().UnixMilli(), SchemaVersion: 1, DeletedKeys: []*labpb.MonitoringDeletedKey{{Kind: "lab", LabGroupName: "group-a", Namespace: "ns-a", Name: "lab-a"}}},
	}}
	runner := NewRunner(nil, func(context.Context, *labpb.MonitoringRequest) (Stream, error) { return stream, nil }, store)
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("consume error = %v, want EOF", err)
	}
	if len(store.appended) != 2 {
		t.Fatalf("stored observations = %d, want snapshot plus deletion", len(store.appended))
	}
	var persisted labpb.MonitoringUpdate
	if err := protojson.Unmarshal(store.appended[1].Payload, &persisted); err != nil {
		t.Fatalf("decode deletion observation: %v", err)
	}
	if got := persisted.GetDeletedKeys(); len(got) != 1 || got[0].GetKind() != "lab" || got[0].GetLabGroupName() != "group-a" || got[0].GetName() != "lab-a" {
		t.Fatalf("stored deletion = %+v", got)
	}
}

func TestConsumeAppliesEveryUpdateToTheCurrentStateAndPrunesOnSnapshots(t *testing.T) {
	store := &recordingStore{}
	now := time.Now().UnixMilli()
	stream := &scriptedStream{updates: []*labpb.MonitoringUpdate{
		{AgentId: "agent-a", Sequence: 1, ObservedAtUnixMs: now, SchemaVersion: 1, Snapshot: true, Groups: []*labpb.LabGroup{{Name: "group-a"}}},
		{AgentId: "agent-a", Sequence: 2, ObservedAtUnixMs: now, SchemaVersion: 1, Labs: []*labpb.Lab{{Name: "web", LabGroupName: "group-a"}}},
	}}
	runner := NewRunner(nil, func(context.Context, *labpb.MonitoringRequest) (Stream, error) { return stream, nil }, store)
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("consume error = %v, want EOF", err)
	}
	if len(store.applied) != 2 || !store.applied[0].snapshot || store.applied[1].snapshot || store.applied[1].sequence != 2 || store.applied[1].group != "group-a" {
		t.Fatalf("current-state applications = %+v, want snapshot then delta for group-a", store.applied)
	}
	if len(store.pruned) != 1 || store.pruned[0] != "agent-a" {
		t.Fatalf("pruned = %v, want exactly one prune after the snapshot", store.pruned)
	}
}

func TestConsumePrunesOnAnEmptySnapshot(t *testing.T) {
	store := &recordingStore{}
	stream := &scriptedStream{updates: []*labpb.MonitoringUpdate{
		{AgentId: "agent-a", Sequence: 1, ObservedAtUnixMs: time.Now().UnixMilli(), SchemaVersion: 1, Snapshot: true},
	}}
	runner := NewRunner(nil, func(context.Context, *labpb.MonitoringRequest) (Stream, error) { return stream, nil }, store)
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("consume error = %v, want EOF", err)
	}
	if len(store.applied) != 0 || len(store.pruned) != 1 {
		t.Fatalf("applied=%v pruned=%v, want a prune and nothing applied", store.applied, store.pruned)
	}
}

func TestWithAgentIDReplacesTheAgentsOwnIDEverywhere(t *testing.T) {
	store := &recordingStore{}
	stream := &scriptedStream{updates: []*labpb.MonitoringUpdate{update("e1", 1, true, "group-a")}}
	var requests []*labpb.MonitoringRequest
	runner := NewRunner(nil, openOnce(stream, &requests), store).WithAgentID("registry-id-1")
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("consume error = %v", err)
	}
	if store.appended[0].AgentID != "registry-id-1" || store.applied[0].agent != "registry-id-1" || store.pruned[0] != "registry-id-1" {
		t.Fatalf("stored under %q / %q / %q, want the registry id", store.appended[0].AgentID, store.applied[0].agent, store.pruned[0])
	}
	other := NewRunner(nil, nil, store).WithAgentID("registry-id-2")
	if other.lockKey == runner.lockKey || other.lockKey == advisoryLockKey {
		t.Fatal("each followed agent leads with its own advisory lock")
	}
}

func TestCapacitySinkReceivesTheReportedCapacity(t *testing.T) {
	store := &recordingStore{}
	stream := &scriptedStream{updates: []*labpb.MonitoringUpdate{{
		AgentId: "agent-a", Sequence: 1, ObservedAtUnixMs: 1_700_000_000_000, SchemaVersion: 1, Snapshot: true,
		Capacity: &labpb.CapacityResponse{Tenant: "platform", HasCpuQuota: true, CpuQuotaMillicores: 8000},
	}}}
	var got *labpb.CapacityResponse
	var at time.Time
	runner := NewRunner(nil, func(context.Context, *labpb.MonitoringRequest) (Stream, error) { return stream, nil }, store).
		WithCapacitySink(func(_ context.Context, c *labpb.CapacityResponse, observed time.Time) error {
			got, at = c, observed
			return io.ErrUnexpectedEOF
		})
	if err := runner.consume(context.Background()); err != io.EOF {
		t.Fatalf("a failing sink must not stop the stream: %v", err)
	}
	if got == nil || got.GetCpuQuotaMillicores() != 8000 || !at.Equal(time.UnixMilli(1_700_000_000_000)) || len(store.capacities) != 1 {
		t.Fatalf("sink got %+v at %v, observations %d", got, at, len(store.capacities))
	}
}
