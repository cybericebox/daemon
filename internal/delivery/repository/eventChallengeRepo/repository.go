// Package eventChallengeRepo maps event board task snapshots to PostgreSQL.
package eventChallengeRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

type Queries interface {
	CreateEventChallenge(ctx context.Context, arg postgres.CreateEventChallengeParams) (postgres.EventChallenge, error)
	ListEventChallenges(ctx context.Context, eventExerciseID uuid.UUID) ([]postgres.EventChallenge, error)
	GetEventContentStatistics(ctx context.Context, arg postgres.GetEventContentStatisticsParams) (postgres.GetEventContentStatisticsRow, error)
	GetEventChallengeByID(ctx context.Context, arg postgres.GetEventChallengeByIDParams) (postgres.EventChallenge, error)
	GetEventChallengeForEvent(ctx context.Context, arg postgres.GetEventChallengeForEventParams) (postgres.EventChallenge, error)
	IsEventChallengePublished(ctx context.Context, id uuid.UUID) (bool, error)
	UpdateEventChallenge(ctx context.Context, arg postgres.UpdateEventChallengeParams) (postgres.EventChallenge, error)
	UpdateEventChallengeScoring(ctx context.Context, arg postgres.UpdateEventChallengeScoringParams) (postgres.EventChallenge, error)
	VacateEventChallengeOrders(ctx context.Context, eventExerciseID uuid.UUID) error
	SetEventChallengeOrder(ctx context.Context, arg postgres.SetEventChallengeOrderParams) (int64, error)
	SetEventChallengeGroup(ctx context.Context, arg postgres.SetEventChallengeGroupParams) (int64, error)
	ListEventGroupChallengeIDs(ctx context.Context, arg postgres.ListEventGroupChallengeIDsParams) ([]uuid.UUID, error)
	SetEventChallengeBoardOrder(ctx context.Context, arg postgres.SetEventChallengeBoardOrderParams) (int64, error)
	ListEventChallengePrerequisites(ctx context.Context, challengeID uuid.UUID) ([]uuid.UUID, error)
	DeleteEventChallengePrerequisites(ctx context.Context, challengeID uuid.UUID) error
	CreateEventChallengePrerequisite(ctx context.Context, arg postgres.CreateEventChallengePrerequisiteParams) error
	UpdateEventChallengeContent(ctx context.Context, arg postgres.UpdateEventChallengeContentParams) (int64, error)
	SetEventChallengeHintCosts(ctx context.Context, arg postgres.SetEventChallengeHintCostsParams) (int64, error)
	ListEventExercisePrerequisites(ctx context.Context, eventExerciseID uuid.UUID) ([]postgres.EventChallengePrerequisite, error)
	ListEventChallengesWithAttempts(ctx context.Context, ids []uuid.UUID) ([]uuid.UUID, error)
	DeleteLabBindingsForChallenges(ctx context.Context, ids []uuid.UUID) error
	DeleteTeamChallengesForChallenges(ctx context.Context, ids []uuid.UUID) error
	DeleteEventChallengesByIDs(ctx context.Context, arg postgres.DeleteEventChallengesByIDsParams) error
	UnpublishEventExerciseChallenges(ctx context.Context, eventExerciseID uuid.UUID) error
	SetEventExerciseChallengesPublished(ctx context.Context, arg postgres.SetEventExerciseChallengesPublishedParams) error
	MaxEventChallengeOrder(ctx context.Context, eventExerciseID uuid.UUID) (int32, error)
}

func (r *Repository) UpdateScoring(ctx context.Context, value eventChallengeModel.EventChallenge) (eventChallengeModel.EventChallenge, error) {
	arg := postgres.UpdateEventChallengeScoringParams{ID: value.ID, EventExerciseID: value.EventExerciseID}
	if p := value.ScoringOverride; p != nil {
		arg.ScoringMode = pgtype.Int2{Int16: int16(p.Mode), Valid: true}
		if p.Mode != eventModel.ScoringStatic {
			arg.DynamicAlgorithm = pgtype.Int2{Int16: int16(p.Mode - 1), Valid: true}
			arg.DynamicMinPoints = pgtype.Int4{Int32: p.MinPoints, Valid: true}
			arg.DynamicMaxPoints = pgtype.Int4{Int32: p.MaxPoints, Valid: true}
			arg.DynamicFloorAtPercent = pgtype.Int4{Int32: p.FloorAtPercent, Valid: true}
		}
	}
	row, err := r.q.UpdateEventChallengeScoring(ctx, arg)
	if err != nil {
		return eventChallengeModel.EventChallenge{}, err
	}
	return ToDomain(row), nil
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Create(ctx context.Context, value eventChallengeModel.EventChallenge) (eventChallengeModel.EventChallenge, error) {
	hints, costs, err := marshalHints(value)
	if err != nil {
		return eventChallengeModel.EventChallenge{}, err
	}
	row, err := r.q.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{ID: value.ID, EventExerciseID: value.EventExerciseID, TaskID: value.TaskID, OrderIndex: value.Order, Points: value.Points, HintsEnabled: value.HintsEnabled, Published: value.Published, Snapshot: value.Snapshot, CreatedAt: value.CreatedAt, Hints: hints, HintCosts: costs})
	if err != nil {
		return eventChallengeModel.EventChallenge{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) List(ctx context.Context, eventExerciseID uuid.UUID) ([]eventChallengeModel.EventChallenge, error) {
	rows, err := r.q.ListEventChallenges(ctx, eventExerciseID)
	if err != nil {
		return nil, err
	}
	items := make([]eventChallengeModel.EventChallenge, 0, len(rows))
	for _, row := range rows {
		items = append(items, ToDomain(row))
	}
	return items, nil
}

// ContentStatistics counts the published tasks and the solves of visible teams for the public page; cutoff,
// when set (results are frozen), leaves out solves made at or after it.
func (r *Repository) ContentStatistics(ctx context.Context, eventID uuid.UUID, cutoff *time.Time) (eventContentModel.Statistics, error) {
	params := postgres.GetEventContentStatisticsParams{EventID: eventID}
	if cutoff != nil {
		params.Cutoff = pgtype.Timestamptz{Time: *cutoff, Valid: true}
	}
	row, err := r.q.GetEventContentStatistics(ctx, params)
	if err != nil {
		return eventContentModel.Statistics{}, err
	}
	return eventContentModel.Statistics{
		ChallengeCount: row.ChallengeCount, PublishedChallengeCount: row.PublishedChallengeCount,
		SolvedChallengeCount: row.SolvedChallengeCount, SolveCount: row.SolveCount,
	}, nil
}

func (r *Repository) GetByID(ctx context.Context, eventExerciseID, id uuid.UUID) (eventChallengeModel.EventChallenge, error) {
	row, err := r.q.GetEventChallengeByID(ctx, postgres.GetEventChallengeByIDParams{ID: id, EventExerciseID: eventExerciseID})
	if err != nil {
		return eventChallengeModel.EventChallenge{}, err
	}
	return ToDomain(row), nil
}

// GetForEvent resolves a board challenge through its owning event. Challenge
// lab routes intentionally accept challenge IDs rather than exercise IDs, so
// this query keeps the event boundary authoritative.
func (r *Repository) GetForEvent(ctx context.Context, eventID, id uuid.UUID) (eventChallengeModel.EventChallenge, error) {
	row, err := r.q.GetEventChallengeForEvent(ctx, postgres.GetEventChallengeForEventParams{ID: id, EventID: eventID})
	if err != nil {
		return eventChallengeModel.EventChallenge{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) Published(ctx context.Context, id uuid.UUID) (bool, error) {
	return r.q.IsEventChallengePublished(ctx, id)
}

func (r *Repository) Update(ctx context.Context, value eventChallengeModel.EventChallenge) (eventChallengeModel.EventChallenge, error) {
	row, err := r.q.UpdateEventChallenge(ctx, postgres.UpdateEventChallengeParams{ID: value.ID, EventExerciseID: value.EventExerciseID, Points: value.Points, HintsEnabled: value.HintsEnabled, Published: value.Published})
	if err != nil {
		return eventChallengeModel.EventChallenge{}, err
	}
	return ToDomain(row), nil
}

func (r *Repository) VacateOrders(ctx context.Context, eventExerciseID uuid.UUID) error {
	return r.q.VacateEventChallengeOrders(ctx, eventExerciseID)
}

func (r *Repository) SetOrder(ctx context.Context, eventExerciseID, id uuid.UUID, order int32) (int64, error) {
	return r.q.SetEventChallengeOrder(ctx, postgres.SetEventChallengeOrderParams{ID: id, EventExerciseID: eventExerciseID, OrderIndex: order})
}

func (r *Repository) SetGroup(ctx context.Context, eventExerciseID, id uuid.UUID, groupID *uuid.UUID) (int64, error) {
	var group uuid.NullUUID
	if groupID != nil {
		group = uuid.NullUUID{UUID: *groupID, Valid: true}
	}
	return r.q.SetEventChallengeGroup(ctx, postgres.SetEventChallengeGroupParams{GroupID: group, ID: id, EventExerciseID: eventExerciseID})
}

// GroupChallengeIDs lists the challenges of the event's active sets in one
// group (nil = no group).
func (r *Repository) GroupChallengeIDs(ctx context.Context, eventID uuid.UUID, groupID *uuid.UUID) ([]uuid.UUID, error) {
	var group uuid.NullUUID
	if groupID != nil {
		group = uuid.NullUUID{UUID: *groupID, Valid: true}
	}
	return r.q.ListEventGroupChallengeIDs(ctx, postgres.ListEventGroupChallengeIDsParams{EventID: eventID, GroupID: group})
}

func (r *Repository) SetBoardOrder(ctx context.Context, eventID, id uuid.UUID, order int32) (int64, error) {
	return r.q.SetEventChallengeBoardOrder(ctx, postgres.SetEventChallengeBoardOrderParams{BoardOrder: pgtype.Int4{Int32: order, Valid: true}, ID: id, EventID: eventID})
}

func (r *Repository) Prerequisites(ctx context.Context, challengeID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.ListEventChallengePrerequisites(ctx, challengeID)
}

func (r *Repository) ReplacePrerequisites(ctx context.Context, challengeID uuid.UUID, prerequisiteIDs []uuid.UUID) error {
	if err := r.q.DeleteEventChallengePrerequisites(ctx, challengeID); err != nil {
		return err
	}
	for _, id := range prerequisiteIDs {
		if err := r.q.CreateEventChallengePrerequisite(ctx, postgres.CreateEventChallengePrerequisiteParams{ChallengeID: challengeID, PrerequisiteChallengeID: id}); err != nil {
			return err
		}
	}
	return nil
}

func ToDomain(row postgres.EventChallenge) eventChallengeModel.EventChallenge {
	var groupID *uuid.UUID
	if row.GroupID.Valid {
		id := row.GroupID.UUID
		groupID = &id
	}
	var override *eventModel.ScoringProfile
	if row.ScoringMode.Valid {
		override = &eventModel.ScoringProfile{Mode: eventModel.ScoringMode(row.ScoringMode.Int16), MinPoints: row.DynamicMinPoints.Int32, MaxPoints: row.DynamicMaxPoints.Int32, FloorAtPercent: row.DynamicFloorAtPercent.Int32}
	}
	var boardOrder *int32
	if row.BoardOrder.Valid {
		value := row.BoardOrder.Int32
		boardOrder = &value
	}
	hints, costs := UnmarshalHints(row.Hints, row.HintCosts)
	return eventChallengeModel.EventChallenge{ID: row.ID, EventExerciseID: row.EventExerciseID, TaskID: row.TaskID, GroupID: groupID, Order: row.OrderIndex, BoardOrder: boardOrder, Points: row.Points, ScoringOverride: override, HintsEnabled: row.HintsEnabled, Published: row.Published, Snapshot: row.Snapshot, Hints: hints, HintCosts: costs, CreatedAt: row.CreatedAt}
}

func marshalHints(value eventChallengeModel.EventChallenge) ([]byte, []byte, error) {
	hints := value.Hints
	if hints == nil {
		hints = []eventChallengeModel.Hint{}
	}
	costs := make(map[string]int32, len(value.HintCosts))
	for id, cost := range value.HintCosts {
		costs[id.String()] = cost
	}
	hintsJSON, err := json.Marshal(hints)
	if err != nil {
		return nil, nil, err
	}
	costsJSON, err := json.Marshal(costs)
	if err != nil {
		return nil, nil, err
	}
	return hintsJSON, costsJSON, nil
}

// UnmarshalHints reads the board hints and cost overrides; corrupt JSON is
// forgiven on read (no hints).
func UnmarshalHints(hintsJSON, costsJSON []byte) ([]eventChallengeModel.Hint, map[uuid.UUID]int32) {
	hints := []eventChallengeModel.Hint{}
	if len(hintsJSON) > 0 {
		if err := json.Unmarshal(hintsJSON, &hints); err != nil {
			hints = []eventChallengeModel.Hint{}
		}
	}
	costs := map[uuid.UUID]int32{}
	raw := map[string]int32{}
	if len(costsJSON) > 0 && json.Unmarshal(costsJSON, &raw) == nil {
		for key, cost := range raw {
			if id, err := uuid.FromString(key); err == nil {
				costs[id] = cost
			}
		}
	}
	return hints, costs
}

// UpdateContent writes a refreshed snapshot and hints (source switch).
func (r *Repository) UpdateContent(ctx context.Context, value eventChallengeModel.EventChallenge) (int64, error) {
	hints, costs, err := marshalHints(value)
	if err != nil {
		return 0, err
	}
	affected, err := r.q.UpdateEventChallengeContent(ctx, postgres.UpdateEventChallengeContentParams{ID: value.ID, EventExerciseID: value.EventExerciseID, Snapshot: value.Snapshot, Hints: hints})
	if err != nil || affected == 0 {
		return affected, err
	}
	return r.q.SetEventChallengeHintCosts(ctx, postgres.SetEventChallengeHintCostsParams{ID: value.ID, EventExerciseID: value.EventExerciseID, HintCosts: costs})
}

// SetHintCosts writes the event's hint cost overrides.
func (r *Repository) SetHintCosts(ctx context.Context, value eventChallengeModel.EventChallenge) (int64, error) {
	_, costs, err := marshalHints(value)
	if err != nil {
		return 0, err
	}
	return r.q.SetEventChallengeHintCosts(ctx, postgres.SetEventChallengeHintCostsParams{ID: value.ID, EventExerciseID: value.EventExerciseID, HintCosts: costs})
}

// PrerequisitesByChallenge returns every prerequisite edge of one board
// revision, keyed by challenge (one query instead of one per challenge).
func (r *Repository) PrerequisitesByChallenge(ctx context.Context, eventExerciseID uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	rows, err := r.q.ListEventExercisePrerequisites(ctx, eventExerciseID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID][]uuid.UUID, len(rows))
	for _, row := range rows {
		out[row.ChallengeID] = append(out[row.ChallengeID], row.PrerequisiteChallengeID)
	}
	return out, nil
}

// WithAttempts returns which of the challenges have any attempt.
func (r *Repository) WithAttempts(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.q.ListEventChallengesWithAttempts(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, id := range rows {
		out[id] = true
	}
	return out, nil
}

// DeleteWithAssignments removes board challenges that have no attempts, with
// their Lab bindings and team assignments (set operation, run in the caller's
// transaction; the order satisfies the RESTRICT foreign keys).
func (r *Repository) DeleteWithAssignments(ctx context.Context, eventExerciseID uuid.UUID, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	if err := r.q.DeleteLabBindingsForChallenges(ctx, ids); err != nil {
		return err
	}
	if err := r.q.DeleteTeamChallengesForChallenges(ctx, ids); err != nil {
		return err
	}
	return r.q.DeleteEventChallengesByIDs(ctx, postgres.DeleteEventChallengesByIDsParams{Ids: ids, EventExerciseID: eventExerciseID})
}

// SetPublishedForExercise shows or hides every challenge of one set.
func (r *Repository) SetPublishedForExercise(ctx context.Context, eventExerciseID uuid.UUID, published bool) error {
	return r.q.SetEventExerciseChallengesPublished(ctx, postgres.SetEventExerciseChallengesPublishedParams{Published: published, EventExerciseID: eventExerciseID})
}

func (r *Repository) UnpublishAll(ctx context.Context, eventExerciseID uuid.UUID) error {
	return r.q.UnpublishEventExerciseChallenges(ctx, eventExerciseID)
}

// MaxOrder is the highest board order of a revision (-1 when empty).
func (r *Repository) MaxOrder(ctx context.Context, eventExerciseID uuid.UUID) (int32, error) {
	return r.q.MaxEventChallengeOrder(ctx, eventExerciseID)
}
