package teamChallengeRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

type Queries interface {
	CreateTeamChallenge(context.Context, postgres.CreateTeamChallengeParams) (postgres.TeamChallenge, error)
	GetTeamChallenge(context.Context, postgres.GetTeamChallengeParams) (postgres.GetTeamChallengeRow, error)
	ListTeamChallenges(context.Context, uuid.UUID) ([]postgres.ListTeamChallengesRow, error)
	ListTeamBoardChallenges(context.Context, postgres.ListTeamBoardChallengesParams) ([]postgres.ListTeamBoardChallengesRow, error)
	ListTeamChallengePrerequisites(context.Context, uuid.UUID) ([]postgres.ListTeamChallengePrerequisitesRow, error)
	CountEventChallengeSolves(context.Context, postgres.CountEventChallengeSolvesParams) ([]postgres.CountEventChallengeSolvesRow, error)
	ListEventChallengeSolves(context.Context, postgres.ListEventChallengeSolvesParams) ([]postgres.ListEventChallengeSolvesRow, error)
	ListFileSizes(context.Context, []uuid.UUID) ([]postgres.ListFileSizesRow, error)
	ListEventChallengeAvailability(context.Context, uuid.UUID) ([]postgres.ListEventChallengeAvailabilityRow, error)
	UpdateTeamChallengeReadiness(context.Context, postgres.UpdateTeamChallengeReadinessParams) (int64, error)
	PublishAvailableTeamChallenges(context.Context, postgres.PublishAvailableTeamChallengesParams) ([]uuid.UUID, error)
	ListTeamChallengesForRefresh(context.Context, []uuid.UUID) ([]postgres.ListTeamChallengesForRefreshRow, error)
	UpdateTeamChallengeContent(context.Context, postgres.UpdateTeamChallengeContentParams) (int64, error)
	GetTeamChallengeHints(context.Context, postgres.GetTeamChallengeHintsParams) (postgres.GetTeamChallengeHintsRow, error)
	CreateHintUnlock(context.Context, postgres.CreateHintUnlockParams) (postgres.CreateHintUnlockRow, error)
	ListTeamHintUnlocks(context.Context, uuid.UUID) ([]postgres.ListTeamHintUnlocksRow, error)
	ListEventHintUnlocks(context.Context, uuid.UUID) ([]postgres.ListEventHintUnlocksRow, error)
}

type Availability struct {
	Preparing, Ready, Available, Failed, Total int64
}

// PublishedChallenge combines the team's pinned, safe task snapshot with the
// event board's current presentation metadata. ExpectedFlag stays in the
// domain object and must never be serialized to participant responses.
type PublishedChallenge struct {
	Challenge  teamChallengeModel.TeamChallenge
	Points     int32
	Order      int32 // the place inside the group (the manage page's group order)
	GroupID    *uuid.UUID
	GroupName  string
	GroupOrder int32
	// ContentUpdatedAt marks a content replacement (W4); nil when never replaced.
	ContentUpdatedAt *time.Time
	HintsEnabled     bool
	// Published is the board publication of the event challenge.
	Published bool
	// Infrastructure: the assignment has a Lab binding.
	Infrastructure bool
	// StageID/StagePhase: the stage of the task's set and its phase at the time the board was read (open without a
	// stage). PracticeSolved: a correct answer given after a returnable stage closed (never rated).
	StageID        *uuid.UUID
	StagePhase     eventModel.StagePhase
	PracticeSolved bool
	// BoardHints/HintCosts: the event challenge's hints and cost overrides;
	// the team's own texts are in Challenge.Hints.
	BoardHints []eventChallengeModel.Hint
	HintCosts  map[uuid.UUID]int32
}

// Prerequisite is one prerequisite of a team's challenge, named by the team's
// own snapshot and marked solved when that team solved it.
type Prerequisite struct {
	EventChallengeID uuid.UUID
	Name             string
	Solved           bool
}

// Solve is one team's accepted solve of a board challenge.
type Solve struct {
	TeamID   uuid.UUID
	TeamName string
	// NameIsReal: TeamName is a participant's real name (individual team, no pseudonym shown).
	NameIsReal      bool
	SolvedAt        time.Time
	TeamChallengeID uuid.UUID
	FirstBlood      bool
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }
func (r *Repository) Create(ctx context.Context, v teamChallengeModel.TeamChallenge) (teamChallengeModel.TeamChallenge, error) {
	hints, err := MarshalHints(v.Hints)
	if err != nil {
		return teamChallengeModel.TeamChallenge{}, err
	}
	row, err := r.q.CreateTeamChallenge(ctx, postgres.CreateTeamChallengeParams{ID: v.ID, EventID: v.EventID, EventTeamID: v.EventTeamID, EventChallengeID: v.EventChallengeID, VariantIndex: v.VariantIndex, Snapshot: v.Snapshot, ExpectedFlag: v.ExpectedFlag, Readiness: int16(v.Readiness), CreatedAt: v.CreatedAt, Hints: hints})
	if err != nil {
		return teamChallengeModel.TeamChallenge{}, err
	}
	created := toDomain(row.ID, row.EventID, row.EventTeamID, row.EventChallengeID, row.VariantIndex, row.Snapshot, row.ExpectedFlag, row.Readiness, pgtype.Timestamptz{}, row.CreatedAt)
	created.Hints = UnmarshalHints(row.Hints)
	return created, nil
}
func (r *Repository) Get(ctx context.Context, teamID, challengeID uuid.UUID) (teamChallengeModel.TeamChallenge, error) {
	row, err := r.q.GetTeamChallenge(ctx, postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID})
	if err != nil {
		return teamChallengeModel.TeamChallenge{}, err
	}
	return toDomain(row.ID, row.EventID, row.EventTeamID, row.EventChallengeID, row.VariantIndex, row.Snapshot, row.ExpectedFlag, row.Readiness, row.SolvedAt, row.CreatedAt), nil
}

func (r *Repository) List(ctx context.Context, teamID uuid.UUID) ([]teamChallengeModel.TeamChallenge, error) {
	rows, err := r.q.ListTeamChallenges(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]teamChallengeModel.TeamChallenge, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row.ID, row.EventID, row.EventTeamID, row.EventChallengeID, row.VariantIndex, row.Snapshot, row.ExpectedFlag, row.Readiness, row.SolvedAt, row.CreatedAt))
	}
	return out, nil
}

// ListPublished returns the participant board: board-published assignments.
func (r *Repository) ListPublished(ctx context.Context, teamID uuid.UUID, at time.Time) ([]PublishedChallenge, error) {
	return r.listBoard(ctx, teamID, true, at)
}

// ListBoard returns every assignment of the team, published on the board or
// not (the moderators board).
func (r *Repository) ListBoard(ctx context.Context, teamID uuid.UUID, at time.Time) ([]PublishedChallenge, error) {
	return r.listBoard(ctx, teamID, false, at)
}

func (r *Repository) listBoard(ctx context.Context, teamID uuid.UUID, publishedOnly bool, at time.Time) ([]PublishedChallenge, error) {
	rows, err := r.q.ListTeamBoardChallenges(ctx, postgres.ListTeamBoardChallengesParams{EventTeamID: teamID, PublishedOnly: publishedOnly, At: at})
	if err != nil {
		return nil, err
	}
	out := make([]PublishedChallenge, 0, len(rows))
	for _, row := range rows {
		var groupID *uuid.UUID
		if row.GroupID.Valid {
			id := row.GroupID.UUID
			groupID = &id
		}
		var contentUpdatedAt *time.Time
		if row.ContentUpdatedAt.Valid {
			at := row.ContentUpdatedAt.Time
			contentUpdatedAt = &at
		}
		challenge := toDomain(row.ID, row.EventID, row.EventTeamID, row.EventChallengeID, row.VariantIndex, row.Snapshot, row.ExpectedFlag, row.Readiness, row.SolvedAt, row.CreatedAt)
		challenge.Hints = UnmarshalHints(row.TeamHints)
		boardHints, costs := eventChallengeRepo.UnmarshalHints(row.BoardHints, row.HintCosts)
		var stageID *uuid.UUID
		if row.StageID.Valid {
			id := row.StageID.UUID
			stageID = &id
		}
		out = append(out, PublishedChallenge{
			StageID: stageID, StagePhase: eventModel.StagePhase(row.StagePhase), PracticeSolved: row.PracticeSolved,
			Challenge: challenge, BoardHints: boardHints, HintCosts: costs,
			Points: row.Points, Order: row.BoardPosition, GroupID: groupID, GroupName: row.GroupName, GroupOrder: row.GroupOrder,
			ContentUpdatedAt: contentUpdatedAt, HintsEnabled: row.HintsEnabled, Published: row.Published, Infrastructure: row.Infrastructure,
		})
	}
	return out, nil
}

// Prerequisites returns the prerequisites of every challenge assigned to the
// team, keyed by the dependent event challenge.
func (r *Repository) Prerequisites(ctx context.Context, teamID uuid.UUID) (map[uuid.UUID][]Prerequisite, error) {
	rows, err := r.q.ListTeamChallengePrerequisites(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID][]Prerequisite)
	for _, row := range rows {
		out[row.ChallengeID] = append(out[row.ChallengeID], Prerequisite{EventChallengeID: row.PrerequisiteChallengeID, Name: row.Name, Solved: row.Solved})
	}
	return out, nil
}

// SolveCounts counts accepted solves per event challenge by admitted,
// non-hidden teams plus ownTeamID.
// A non-nil cutoff (freeze) keeps only other teams' solves before it.
func (r *Repository) SolveCounts(ctx context.Context, eventID, ownTeamID uuid.UUID, cutoff *time.Time) (map[uuid.UUID]int64, error) {
	rows, err := r.q.CountEventChallengeSolves(ctx, postgres.CountEventChallengeSolvesParams{EventID: eventID, OwnTeamID: ownTeamID, Cutoff: cutoffParam(cutoff)})
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int64, len(rows))
	for _, row := range rows {
		out[row.EventChallengeID] = row.Solves
	}
	return out, nil
}

// Solves lists one page of who solved an event challenge (same population as
// SolveCounts), oldest solve first, after the cursor. It returns at most limit
// rows; the caller asks for one more to know whether a next page exists.
func (r *Repository) Solves(ctx context.Context, eventID, challengeID, ownTeamID uuid.UUID, cutoff *time.Time, after uuid.UUID, limit int32) ([]Solve, error) {
	params := postgres.ListEventChallengeSolvesParams{EventID: eventID, EventChallengeID: challengeID, OwnTeamID: ownTeamID, Cutoff: cutoffParam(cutoff), RowLimit: limit}
	if after != uuid.Nil {
		params.AfterID = uuid.NullUUID{UUID: after, Valid: true}
	}
	rows, err := r.q.ListEventChallengeSolves(ctx, params)
	if err != nil {
		return nil, err
	}
	out := make([]Solve, 0, len(rows))
	for _, row := range rows {
		out = append(out, Solve{TeamID: row.EventTeamID, TeamName: row.TeamName, NameIsReal: row.NameIsReal, SolvedAt: row.SolvedAt, TeamChallengeID: row.TeamChallengeID, FirstBlood: row.FirstBlood})
	}
	return out, nil
}

// FileSizes looks up attachment sizes in one query; unknown IDs are absent.
func (r *Repository) FileSizes(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	out := make(map[uuid.UUID]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.q.ListFileSizes(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.ID] = row.SizeBytes
	}
	return out, nil
}

func (r *Repository) AvailabilityForExercise(ctx context.Context, eventExerciseID uuid.UUID) (map[uuid.UUID]Availability, error) {
	rows, err := r.q.ListEventChallengeAvailability(ctx, eventExerciseID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]Availability, len(rows))
	for _, row := range rows {
		out[row.EventChallengeID] = Availability{Preparing: row.Preparing, Ready: row.Ready, Available: row.Available, Failed: row.Failed, Total: row.Total}
	}
	return out, nil
}

func (r *Repository) UpdateReadiness(ctx context.Context, id uuid.UUID, expected, next teamChallengeModel.Readiness) (int64, error) {
	return r.q.UpdateTeamChallengeReadiness(ctx, postgres.UpdateTeamChallengeReadinessParams{ID: id, ExpectedReadiness: int16(expected), Readiness: int16(next)})
}

// PublishAvailable moves ready assignments of board-published challenges to
// published: static ones always, infrastructure ones only when labsOpen (the
// event's strict barrier). It returns the distinct affected teams.
func (r *Repository) PublishAvailable(ctx context.Context, eventID uuid.UUID, labsOpen bool) ([]uuid.UUID, error) {
	rows, err := r.q.PublishAvailableTeamChallenges(ctx, postgres.PublishAvailableTeamChallengesParams{EventID: eventID, LabsOpen: labsOpen})
	if err != nil {
		return nil, err
	}
	seen := make(map[uuid.UUID]struct{}, len(rows))
	teams := make([]uuid.UUID, 0, len(rows))
	for _, teamID := range rows {
		if _, ok := seen[teamID]; ok {
			continue
		}
		seen[teamID] = struct{}{}
		teams = append(teams, teamID)
	}
	return teams, nil
}
func toDomain(id, eventID, teamID, eventChallengeID uuid.UUID, variantIndex int32, snapshot []byte, expectedFlag string, readiness int16, solvedAt pgtype.Timestamptz, createdAt time.Time) teamChallengeModel.TeamChallenge {
	var solved *time.Time
	if solvedAt.Valid {
		x := solvedAt.Time
		solved = &x
	}
	return teamChallengeModel.TeamChallenge{ID: id, EventID: eventID, EventTeamID: teamID, EventChallengeID: eventChallengeID, VariantIndex: variantIndex, Snapshot: snapshot, ExpectedFlag: expectedFlag, Readiness: teamChallengeModel.Readiness(readiness), SolvedAt: solved, CreatedAt: createdAt}
}

func cutoffParam(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

// MarshalHints encodes a team's hint texts (never nil in SQL).
func MarshalHints(hints []teamChallengeModel.Hint) ([]byte, error) {
	if hints == nil {
		hints = []teamChallengeModel.Hint{}
	}
	return json.Marshal(hints)
}

// UnmarshalHints decodes a team's hint texts; corrupt JSON reads as none.
func UnmarshalHints(raw []byte) []teamChallengeModel.Hint {
	hints := []teamChallengeModel.Hint{}
	if len(raw) > 0 && json.Unmarshal(raw, &hints) != nil {
		return []teamChallengeModel.Hint{}
	}
	return hints
}

// RefreshRow is one team assignment whose source content is switched.
type RefreshRow struct {
	ID               uuid.UUID
	EventTeamID      uuid.UUID
	EventChallengeID uuid.UUID
	VariantIndex     int32
	Snapshot         json.RawMessage
	Hints            []teamChallengeModel.Hint
	ExpectedFlag     string
	Solved           bool
}

// ForRefresh locks and returns the team assignments of board challenges.
func (r *Repository) ForRefresh(ctx context.Context, challengeIDs []uuid.UUID) ([]RefreshRow, error) {
	if len(challengeIDs) == 0 {
		return []RefreshRow{}, nil
	}
	rows, err := r.q.ListTeamChallengesForRefresh(ctx, challengeIDs)
	if err != nil {
		return nil, err
	}
	out := make([]RefreshRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, RefreshRow{ID: row.ID, EventTeamID: row.EventTeamID, EventChallengeID: row.EventChallengeID, VariantIndex: row.VariantIndex,
			Snapshot: row.Snapshot, Hints: UnmarshalHints(row.Hints), ExpectedFlag: row.ExpectedFlag, Solved: row.Solved})
	}
	return out, nil
}

// UpdateContent writes a refreshed assignment; changed stamps
// content_updated_at (the board's «Оновлено»).
func (r *Repository) UpdateContent(ctx context.Context, row RefreshRow, changed bool, now time.Time) (int64, error) {
	hints, err := MarshalHints(row.Hints)
	if err != nil {
		return 0, err
	}
	return r.q.UpdateTeamChallengeContent(ctx, postgres.UpdateTeamChallengeContentParams{ID: row.ID, VariantIndex: row.VariantIndex, Snapshot: row.Snapshot,
		Hints: hints, ExpectedFlag: row.ExpectedFlag, ContentChanged: changed, Now: now})
}

// HintState is one assignment with what an unlock needs, locked for update.
type HintState struct {
	TeamChallengeID  uuid.UUID
	EventID          uuid.UUID
	EventTeamID      uuid.UUID
	EventChallengeID uuid.UUID
	Readiness        teamChallengeModel.Readiness
	TeamHints        []teamChallengeModel.Hint
	BoardHints       []eventChallengeModel.Hint
	HintCosts        map[uuid.UUID]int32
	HintsEnabled     bool
	Published        bool
	Solved           bool
	// StagePhase is the phase of the task's set at the time the state was read.
	StagePhase eventModel.StagePhase
}

func (r *Repository) HintState(ctx context.Context, teamID, challengeID uuid.UUID, at time.Time) (HintState, error) {
	row, err := r.q.GetTeamChallengeHints(ctx, postgres.GetTeamChallengeHintsParams{EventTeamID: teamID, EventChallengeID: challengeID, At: at})
	if err != nil {
		return HintState{}, err
	}
	boardHints, costs := eventChallengeRepo.UnmarshalHints(row.BoardHints, row.HintCosts)
	return HintState{TeamChallengeID: row.ID, EventID: row.EventID, EventTeamID: row.EventTeamID, EventChallengeID: row.EventChallengeID,
		Readiness: teamChallengeModel.Readiness(row.Readiness), TeamHints: UnmarshalHints(row.TeamHints), BoardHints: boardHints, HintCosts: costs,
		HintsEnabled: row.HintsEnabled, Published: row.Published, Solved: row.Solved, StagePhase: eventModel.StagePhase(row.StagePhase)}, nil
}

// HintUnlock is one team's unlock of one hint.
type HintUnlock struct {
	TeamChallengeID  uuid.UUID
	EventChallengeID uuid.UUID
	EventTeamID      uuid.UUID
	HintID           uuid.UUID
	UnlockedBy       uuid.NullUUID
	UnlockedByName   string
	UnlockedAt       time.Time
	Cost             int32
	TeamName         string
	ChallengeName    string
	HintIndex        int
	// Created is false when the team had already unlocked the hint.
	Created bool
}

// Unlock records an unlock once per team and hint; a repeat returns the
// first record with Created = false.
func (r *Repository) Unlock(ctx context.Context, state HintState, hintID, by uuid.UUID, at time.Time, cost int32) (HintUnlock, error) {
	row, err := r.q.CreateHintUnlock(ctx, postgres.CreateHintUnlockParams{TeamChallengeID: state.TeamChallengeID, HintID: hintID, EventID: state.EventID,
		EventTeamID: state.EventTeamID, EventChallengeID: state.EventChallengeID, UnlockedBy: uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}, UnlockedAt: at, Cost: cost})
	if err != nil {
		return HintUnlock{}, err
	}
	return HintUnlock{TeamChallengeID: row.TeamChallengeID, EventChallengeID: state.EventChallengeID, EventTeamID: state.EventTeamID, HintID: row.HintID,
		UnlockedBy: row.UnlockedBy, UnlockedAt: row.UnlockedAt, Cost: row.Cost, Created: row.Created}, nil
}

// TeamUnlocks lists a team's unlocks with the member's name.
func (r *Repository) TeamUnlocks(ctx context.Context, teamID uuid.UUID) ([]HintUnlock, error) {
	rows, err := r.q.ListTeamHintUnlocks(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]HintUnlock, 0, len(rows))
	for _, row := range rows {
		out = append(out, HintUnlock{TeamChallengeID: row.TeamChallengeID, EventChallengeID: row.EventChallengeID, EventTeamID: teamID, HintID: row.HintID,
			UnlockedAt: row.UnlockedAt, Cost: row.Cost, UnlockedByName: row.UnlockedByName})
	}
	return out, nil
}

// EventUnlocks lists who unlocked which hint in an event (newest first).
func (r *Repository) EventUnlocks(ctx context.Context, eventID uuid.UUID) ([]HintUnlock, error) {
	rows, err := r.q.ListEventHintUnlocks(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]HintUnlock, 0, len(rows))
	for _, row := range rows {
		index := -1
		hints, _ := eventChallengeRepo.UnmarshalHints(row.BoardHints, nil)
		for i, hint := range hints {
			if hint.ID == row.HintID {
				index = i
			}
		}
		out = append(out, HintUnlock{EventChallengeID: row.EventChallengeID, EventTeamID: row.EventTeamID, HintID: row.HintID, UnlockedBy: row.UnlockedBy,
			UnlockedByName: row.UnlockedByName, UnlockedAt: row.UnlockedAt, Cost: row.Cost, TeamName: row.TeamName, ChallengeName: row.ChallengeName, HintIndex: index})
	}
	return out, nil
}
