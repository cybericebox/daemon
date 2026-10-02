// Package lab runs the optional, singleton Laboratory monitoring subscription.
package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
	"google.golang.org/protobuf/encoding/protojson"

	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
)

const advisoryLockKey int64 = 0x434943454c41424d // "CICELABM"

type Stream interface {
	Recv() (*labpb.MonitoringUpdate, error)
}
type OpenStream func(context.Context, *labpb.MonitoringRequest) (Stream, error)
type Store interface {
	ActiveTeams(context.Context, string, time.Time) ([]labMonitoringModel.EventTeam, error)
	Append(context.Context, labMonitoringModel.Observation) (labMonitoringModel.Observation, error)
	AppendCapacity(context.Context, labMonitoringModel.CapacityObservation) (labMonitoringModel.CapacityObservation, error)
	// ApplyCurrent folds one per-group update into the merged current state
	// (snapshots replace, deltas merge); PruneCurrent drops the agent's groups
	// that a snapshot taken at `before` no longer reports.
	ApplyCurrent(ctx context.Context, labGroupName, agentID string, sequence int64, observedAt, updatedAt time.Time, snapshot bool, payload json.RawMessage) error
	PruneCurrent(ctx context.Context, agentID string, before time.Time) error
}

type Runner struct {
	pool    *pgxpool.Pool
	open    OpenStream
	store   Store
	traffic TrafficSink
	// selector is the Kubernetes label selector of the subscription (this platform instance's objects).
	selector string
	// agentID, when set, replaces the agent's own id in everything stored: with several agents it is
	// the platform's registry id of the agent, so the state of each is kept apart. lockKey is the
	// advisory lock this runner leads with, one per agent.
	agentID string
	lockKey int64
	// capacity receives every capacity the agent reports (its tenant view); a failure is logged, never fatal.
	capacity CapacitySink
	// features receives what the agent says the tenant can use (first message, then on change).
	features FeaturesSink
	// link hears whether the stream to the agent works (the error journal's agent-offline check).
	link LinkSink
	// position is the last message processed: where a reconnect resumes. It lives in memory for the
	// runner's lifetime; a restarted daemon starts with a snapshot.
	position position
}

// position is a place in the agent's stream: sequences grow within one epoch (one agent process)
// and may skip numbers, so only the pair identifies where to resume.
type position struct {
	epoch    string
	sequence int64
}

func NewRunner(pool *pgxpool.Pool, open OpenStream, store Store) *Runner {
	return &Runner{pool: pool, open: open, store: store, lockKey: advisoryLockKey}
}

// CapacitySink keeps the capacity an agent reports.
type CapacitySink func(ctx context.Context, capacity *labpb.CapacityResponse, observedAt time.Time) error

// FeaturesSink keeps the features an agent reports.
type FeaturesSink func(ctx context.Context, features *labpb.FeaturesResponse, observedAt time.Time) error

// LinkSink hears the state of the monitoring link: Down when the stream could not be opened or broke, Up when
// the agent delivered a message. A sink must not block.
type LinkSink interface {
	Down(cause error)
	Up()
}

// WithLinkSink reports the state of the link to sink.
func (r *Runner) WithLinkSink(sink LinkSink) *Runner {
	r.link = sink
	return r
}

// WithFeaturesSink hands every reported features message to sink.
func (r *Runner) WithFeaturesSink(sink FeaturesSink) *Runner {
	r.features = sink
	return r
}

// WithCapacitySink hands every reported capacity to sink (the recorded capacity of the agent).
func (r *Runner) WithCapacitySink(sink CapacitySink) *Runner {
	r.capacity = sink
	return r
}

// WithAgentID names the agent the runner follows; everything it stores carries this id instead of the
// one the agent reports about itself, and it leads with a lock of its own so several agents can be
// followed at once.
func (r *Runner) WithAgentID(id string) *Runner {
	r.agentID = id
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	r.lockKey = advisoryLockKey ^ int64(h.Sum32())
	return r
}

// WithSelector subscribes only to the objects matching the label selector (the platform-instance
// label), so two platform instances can share one cluster.
func (r *Runner) WithSelector(selector string) *Runner {
	r.selector = selector
	return r
}

// Run waits for leadership, holds the advisory lock for the live stream, and
// reconnects with bounded backoff. A new stream must begin with a snapshot.
func (r *Runner) Run(ctx context.Context) error {
	backoff := time.Second
	for ctx.Err() == nil {
		conn, err := r.pool.Acquire(ctx)
		if err != nil {
			return fmt.Errorf("acquire monitoring lock connection: %w", err)
		}
		var locked bool
		err = conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", r.lockKey).Scan(&locked)
		if err != nil {
			conn.Release()
			return fmt.Errorf("acquire monitoring lock: %w", err)
		}
		if !locked {
			conn.Release()
			if !wait(ctx, 5*time.Second) {
				return nil
			}
			continue
		}
		err = r.consume(ctx)
		_, _ = conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", r.lockKey)
		conn.Release()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return nil
		}
		if r.link != nil && err != nil {
			r.link.Down(err)
		}
		if !wait(ctx, backoff) {
			return nil
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
	return nil
}

// consume follows one stream. A reconnect resumes after the last processed position: the agent then
// replays what was missed (snapshot=false) or, when it cannot (it restarted, or the journal no longer
// reaches back), starts with a full snapshot. Sequences may skip numbers (updates that did not match
// the selector), so only the epoch and the order are checked, never sequence+1.
func (r *Runner) consume(ctx context.Context) error {
	request := &labpb.MonitoringRequest{Selector: r.selector}
	resuming := r.position.epoch != ""
	if resuming {
		request.AgentEpoch, request.ResumeAfterSequence = r.position.epoch, r.position.sequence
	}
	stream, err := r.open(ctx, request)
	if err != nil {
		return err
	}
	needSnapshot := !resuming
	linkUp := false
	for {
		update, err := stream.Recv()
		if err != nil {
			return err
		}
		if !linkUp && r.link != nil {
			linkUp = true
			r.link.Up()
		}
		if r.agentID != "" {
			update.AgentId = r.agentID
		}
		if update.GetSnapshot() {
			needSnapshot = false
		} else if needSnapshot || update.GetAgentEpoch() != r.position.epoch || update.GetSequence() < r.position.sequence {
			// Without a snapshot we hold nothing to apply a delta to; a new epoch is a new agent process.
			r.position = position{}
			return fmt.Errorf("monitoring stream requires a fresh snapshot")
		}
		if err := r.persist(ctx, update); err != nil {
			return err
		}
		r.position = position{epoch: update.GetAgentEpoch(), sequence: update.GetSequence()}
	}
}

func (r *Runner) persist(ctx context.Context, update *labpb.MonitoringUpdate) error {
	if features := update.GetFeatures(); features != nil && r.features != nil {
		if sinkErr := r.features(ctx, features, time.UnixMilli(update.GetObservedAtUnixMs()).UTC()); sinkErr != nil {
			log.Error().Err(sinkErr).Msg("Failed to record the agent features")
		}
	}
	if capacity := update.GetCapacity(); capacity != nil {
		encoded, err := protojson.Marshal(capacity)
		if err != nil {
			return fmt.Errorf("marshal capacity payload: %w", err)
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		if r.capacity != nil {
			if sinkErr := r.capacity(ctx, capacity, time.UnixMilli(update.GetObservedAtUnixMs()).UTC()); sinkErr != nil {
				log.Error().Err(sinkErr).Msg("Failed to record the agent capacity")
			}
		}
		_, err = r.store.AppendCapacity(ctx, labMonitoringModel.CapacityObservation{
			ID: id, AgentID: update.GetAgentId(), Sequence: update.GetSequence(),
			ObservedAt: time.UnixMilli(update.GetObservedAtUnixMs()).UTC(), ReceivedAt: time.Now().UTC(),
			SchemaVersion: int32(update.GetSchemaVersion()), Snapshot: update.GetSnapshot(), Payload: encoded,
		})
		if err != nil {
			return err
		}
	}
	if reports := update.GetTraffic(); len(reports) > 0 && r.traffic != nil {
		if err := r.traffic.ApplyTraffic(ctx, reports); err != nil {
			return fmt.Errorf("apply lab traffic: %w", err)
		}
	}
	receivedAt := time.Now().UTC()
	if len(update.GetGroups()) == 0 && len(update.GetLabs()) == 0 && len(update.GetClients()) == 0 && len(update.GetPolicies()) == 0 && len(update.GetDeletedKeys()) == 0 {
		// An empty snapshot still means "nothing exists any more".
		return r.pruneAfterSnapshot(ctx, update, receivedAt)
	}
	observedAt := time.UnixMilli(update.GetObservedAtUnixMs()).UTC()
	for groupName, payload := range splitByGroup(update) {
		encoded, err := protojson.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal monitoring payload: %w", err)
		}
		if err = r.store.ApplyCurrent(ctx, groupName, update.GetAgentId(), update.GetSequence(), observedAt, receivedAt, update.GetSnapshot(), encoded); err != nil {
			return err
		}
		teams, err := r.store.ActiveTeams(ctx, groupName, observedAt)
		if err != nil {
			return err
		}
		if len(teams) == 0 {
			continue
		}
		for _, team := range teams {
			id, err := uuid.NewV7()
			if err != nil {
				return err
			}
			_, err = r.store.Append(ctx, labMonitoringModel.Observation{ID: id, EventID: team.EventID, EventTeamID: team.EventTeamID, LabGroupName: groupName, AgentID: update.GetAgentId(), Sequence: update.GetSequence(), ObservedAt: observedAt, ReceivedAt: receivedAt, SchemaVersion: int32(update.GetSchemaVersion()), Snapshot: update.GetSnapshot(), Payload: encoded})
			if err != nil {
				return err
			}
		}
	}
	return r.pruneAfterSnapshot(ctx, update, receivedAt)
}

func (r *Runner) pruneAfterSnapshot(ctx context.Context, update *labpb.MonitoringUpdate, receivedAt time.Time) error {
	if !update.GetSnapshot() {
		return nil
	}
	return r.store.PruneCurrent(ctx, update.GetAgentId(), receivedAt)
}

func splitByGroup(update *labpb.MonitoringUpdate) map[string]*labpb.MonitoringUpdate {
	parts := map[string]*labpb.MonitoringUpdate{}
	get := func(group string) *labpb.MonitoringUpdate {
		if parts[group] == nil {
			parts[group] = &labpb.MonitoringUpdate{AgentId: update.GetAgentId(), Sequence: update.GetSequence(), ObservedAtUnixMs: update.GetObservedAtUnixMs(), SchemaVersion: update.GetSchemaVersion(), Snapshot: update.GetSnapshot()}
		}
		return parts[group]
	}
	for _, group := range update.GetGroups() {
		get(group.GetName()).Groups = append(get(group.GetName()).Groups, group)
	}
	for _, lab := range update.GetLabs() {
		get(lab.GetLabGroupName()).Labs = append(get(lab.GetLabGroupName()).Labs, lab)
	}
	for _, client := range update.GetClients() {
		get(client.GetLabGroupName()).Clients = append(get(client.GetLabGroupName()).Clients, client)
	}
	for _, policy := range update.GetPolicies() {
		get(policy.GetLabGroupName()).Policies = append(get(policy.GetLabGroupName()).Policies, policy)
	}
	for _, deleted := range update.GetDeletedKeys() {
		get(deleted.GetLabGroupName()).DeletedKeys = append(get(deleted.GetLabGroupName()).DeletedKeys, deleted)
	}
	return parts
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
