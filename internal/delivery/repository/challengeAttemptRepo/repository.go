package challengeAttemptRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

type Queries interface {
	CreateChallengeAttempt(context.Context, postgres.CreateChallengeAttemptParams) (postgres.ChallengeAttempt, error)
	GetEventSolutionAttemptForDecision(context.Context, postgres.GetEventSolutionAttemptForDecisionParams) (postgres.GetEventSolutionAttemptForDecisionRow, error)
	CreateChallengeAttemptDecision(context.Context, postgres.CreateChallengeAttemptDecisionParams) (postgres.ChallengeAttemptDecision, error)
	GetEffectiveTeamChallengeSolvedAt(context.Context, uuid.UUID) (postgres.GetEffectiveTeamChallengeSolvedAtRow, error)
	GetTeamChallengeScoringContext(context.Context, uuid.UUID) (postgres.GetTeamChallengeScoringContextRow, error)
	UpsertTeamChallengeSolve(context.Context, postgres.UpsertTeamChallengeSolveParams) error
	UpsertTeamChallengePracticeSolve(context.Context, postgres.UpsertTeamChallengePracticeSolveParams) error
	DeleteTeamChallengeSolve(context.Context, uuid.UUID) error
	ListEventSolutionAttempts(context.Context, postgres.ListEventSolutionAttemptsParams) ([]postgres.ListEventSolutionAttemptsRow, error)
	ListTeamResultAttempts(context.Context, postgres.ListTeamResultAttemptsParams) ([]postgres.ListTeamResultAttemptsRow, error)
	CountEventSolutionAttempts(context.Context, postgres.CountEventSolutionAttemptsParams) (int64, error)
	GetEventSolutionAttemptCursor(context.Context, postgres.GetEventSolutionAttemptCursorParams) (postgres.GetEventSolutionAttemptCursorRow, error)
	LockEventTeamChallenge(context.Context, postgres.LockEventTeamChallengeParams) (uuid.UUID, error)
	ListEffectiveCorrectAttemptIDs(context.Context, uuid.UUID) ([]uuid.UUID, error)
	GetEventAttemptsStamp(context.Context, uuid.UUID) (postgres.GetEventAttemptsStampRow, error)
	GetTeamChallengeAttemptWindow(context.Context, postgres.GetTeamChallengeAttemptWindowParams) (postgres.GetTeamChallengeAttemptWindowRow, error)
	GetTeamAttemptWindow(context.Context, postgres.GetTeamAttemptWindowParams) (postgres.GetTeamAttemptWindowRow, error)
	GetTeamChallengeAttemptLimit(context.Context, uuid.UUID) (postgres.GetTeamChallengeAttemptLimitRow, error)
	ListTeamChallengeAttemptLimits(context.Context, uuid.UUID) ([]postgres.ListTeamChallengeAttemptLimitsRow, error)
}

// LimitState is where a team stands against the flag attempt limit of one task: Max is the effective limit (the
// task's own, else the event's; nil = unlimited), Wrong the counted wrong submissions, Solved whether the team
// already solved the task (nothing is counted or limited after that).
type LimitState struct {
	Max    *int32
	Wrong  int64
	Solved bool
}

func limitState(max pgtype.Int4, wrong int64, solved bool) LimitState {
	state := LimitState{Wrong: wrong, Solved: solved}
	if max.Valid {
		value := max.Int32
		state.Max = &value
	}
	return state
}

// AttemptLimit reads the attempt limit state of one team challenge.
func (r *Repository) AttemptLimit(ctx context.Context, teamChallengeID uuid.UUID) (LimitState, error) {
	row, err := r.q.GetTeamChallengeAttemptLimit(ctx, teamChallengeID)
	if err != nil {
		return LimitState{}, err
	}
	return limitState(row.MaxAttempts, row.Wrong, row.Solved), nil
}

// AttemptLimits reads the state of every limited team challenge of a team, keyed by team challenge ID.
func (r *Repository) AttemptLimits(ctx context.Context, teamID uuid.UUID) (map[uuid.UUID]LimitState, error) {
	rows, err := r.q.ListTeamChallengeAttemptLimits(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]LimitState, len(rows))
	for _, row := range rows {
		out[row.TeamChallengeID] = limitState(row.MaxAttempts, row.Wrong, row.Solved)
	}
	return out, nil
}

// Window is the rate-limit view of recent submissions: how many of the
// latest ones fall inside the window (capped at the limit) and the oldest.
type Window struct {
	Attempts int64
	Oldest   time.Time
}

// ChallengeWindow counts one team challenge's attempts inside limit's window.
func (r *Repository) ChallengeWindow(ctx context.Context, teamChallengeID uuid.UUID, limit challengeAttempt.RateLimit, now time.Time) (Window, error) {
	row, err := r.q.GetTeamChallengeAttemptWindow(ctx, postgres.GetTeamChallengeAttemptWindowParams{TeamChallengeID: teamChallengeID, Since: limit.Since(now), RowLimit: limit.Attempts})
	return Window{Attempts: row.Attempts, Oldest: row.Oldest}, err
}

// TeamWindow counts the attempts of one team over all its challenges.
func (r *Repository) TeamWindow(ctx context.Context, eventID, teamID uuid.UUID, limit challengeAttempt.RateLimit, now time.Time) (Window, error) {
	row, err := r.q.GetTeamAttemptWindow(ctx, postgres.GetTeamAttemptWindowParams{EventID: eventID, EventTeamID: teamID, Since: limit.Since(now), RowLimit: limit.Attempts})
	return Window{Attempts: row.Attempts, Oldest: row.Oldest}, err
}

// LockTeamChallenge locks the (team, event challenge) row inside the caller's
// transaction and returns its ID.
func (r *Repository) LockTeamChallenge(ctx context.Context, eventID, teamID, challengeID uuid.UUID) (uuid.UUID, error) {
	return r.q.LockEventTeamChallenge(ctx, postgres.LockEventTeamChallengeParams{EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID})
}

// EffectiveCorrectAttempts lists the attempts that currently count as correct.
func (r *Repository) EffectiveCorrectAttempts(ctx context.Context, teamChallengeID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.ListEffectiveCorrectAttemptIDs(ctx, teamChallengeID)
}

// Stamp is a change marker of an event's attempts journal: new attempts and
// new decisions both move it.
type Stamp struct {
	Attempts, Decisions, HintUnlocks int64
}

func (r *Repository) Stamp(ctx context.Context, eventID uuid.UUID) (Stamp, error) {
	row, err := r.q.GetEventAttemptsStamp(ctx, eventID)
	return Stamp{Attempts: row.Attempts, Decisions: row.Decisions, HintUnlocks: row.HintUnlocks}, err
}

type ScoringContext struct {
	EventID, EventChallengeID uuid.UUID
	StaticPoints              int32
	EventProfile              eventModel.ScoringProfile
	LocalProfile              *eventModel.ScoringProfile
	ForceEventScoring         bool
	Population                int32
	StartAt                   time.Time
	FinishAt                  *time.Time
}

func (r *Repository) GetScoringContext(ctx context.Context, teamChallengeID uuid.UUID) (ScoringContext, error) {
	row, err := r.q.GetTeamChallengeScoringContext(ctx, teamChallengeID)
	if err != nil {
		return ScoringContext{}, err
	}
	value := ScoringContext{EventID: row.EventID, EventChallengeID: row.EventChallengeID, StaticPoints: row.StaticPoints,
		EventProfile:      eventModel.ScoringProfile{Mode: eventModel.ScoringMode(row.EventScoringMode), MinPoints: row.EventMinPoints, MaxPoints: row.EventMaxPoints, FloorAtPercent: row.EventFloorAtPercent},
		ForceEventScoring: row.ForceEventScoring, StartAt: row.StartAt}
	if row.LocalScoringMode.Valid {
		value.LocalProfile = &eventModel.ScoringProfile{Mode: eventModel.ScoringMode(row.LocalScoringMode.Int16), MinPoints: row.LocalMinPoints.Int32, MaxPoints: row.LocalMaxPoints.Int32, FloorAtPercent: row.LocalFloorAtPercent.Int32}
	}
	if row.UnitsCount.Valid {
		value.Population = row.UnitsCount.Int32
	}
	if row.FinishAt.Valid {
		finish := row.FinishAt.Time
		value.FinishAt = &finish
	}
	// A staged set decays over its stage window, an unstaged one over the event window.
	if row.StageOpensAt.Valid {
		value.StartAt = row.StageOpensAt.Time
	}
	if row.StageClosesAt.Valid {
		closes := row.StageClosesAt.Time
		value.FinishAt = &closes
	}
	return value, nil
}

type TeamResultAttempt struct {
	ID, EventTeamID, UserID, TeamChallengeID, EventChallengeID uuid.UUID
	ParticipantName, Answer                                    string
	AutomaticCorrect, Correct                                  bool
	Decision                                                   challengeAttempt.Decision
	ReceivedAt                                                 time.Time
	Practice                                                   bool
}

func (r *Repository) ListTeamResults(ctx context.Context, eventID, teamID uuid.UUID) ([]TeamResultAttempt, error) {
	rows, err := r.q.ListTeamResultAttempts(ctx, postgres.ListTeamResultAttemptsParams{EventID: eventID, EventTeamID: teamID})
	if err != nil {
		return nil, err
	}
	out := make([]TeamResultAttempt, 0, len(rows))
	for _, row := range rows {
		out = append(out, TeamResultAttempt{ID: row.ID, EventTeamID: row.EventTeamID, UserID: row.UserID, ParticipantName: row.ParticipantName, TeamChallengeID: row.TeamChallengeID, EventChallengeID: row.EventChallengeID, Answer: row.Answer, AutomaticCorrect: row.AutomaticCorrect, Correct: row.Correct, Decision: challengeAttempt.Decision(row.Decision), ReceivedAt: row.ReceivedAt, Practice: row.Practice})
	}
	return out, nil
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// RecordPracticeSolve notes a correct answer given after a returnable stage closed. It never touches the solves
// the rating reads.
func (r *Repository) RecordPracticeSolve(ctx context.Context, teamChallengeID uuid.UUID, at time.Time) error {
	return r.q.UpsertTeamChallengePracticeSolve(ctx, postgres.UpsertTeamChallengePracticeSolveParams{TeamChallengeID: teamChallengeID, SolvedAt: at})
}

func (r *Repository) Create(ctx context.Context, value challengeAttempt.Attempt) (challengeAttempt.Attempt, error) {
	row, err := r.q.CreateChallengeAttempt(ctx, postgres.CreateChallengeAttemptParams{ID: value.ID, EventID: value.EventID, EventTeamID: value.EventTeamID, TeamChallengeID: value.TeamChallengeID, UserID: value.UserID, Answer: value.Answer, Correct: value.Correct, ReceivedAt: value.ReceivedAt, CreatedAt: value.CreatedAt, Practice: value.Practice})
	if err != nil {
		return challengeAttempt.Attempt{}, err
	}
	return ToDomain(row), nil
}

type ListFilter struct {
	EventID          uuid.UUID
	TeamID           *uuid.UUID
	ParticipantID    *uuid.UUID
	ChallengeID      *uuid.UUID
	Correct          *bool
	FromAt, ToAt     *time.Time
	CursorReceivedAt time.Time
	CursorID         uuid.UUID
	Limit            int32
}

type SolutionAttempt struct {
	ID, EventID, EventTeamID, TeamChallengeID, EventChallengeID, EventExerciseID, UserID uuid.UUID
	TeamName, ChallengeName, ParticipantName, Answer, ExpectedFlag                       string
	AutomaticCorrect, Correct                                                            bool
	Decision                                                                             challengeAttempt.Decision
	DecisionReason                                                                       *string
	DecidedBy                                                                            *uuid.UUID
	DecidedAt                                                                            *time.Time
	ReceivedAt                                                                           time.Time
	// Points is set only on the attempt that solved the task (the earliest
	// effective correct one): the task's live points for that team.
	Points *int32
	// AttemptsAllowed is the effective flag attempt limit of the task (nil = unlimited) and AttemptsUsed the
	// team's counted wrong submissions on it.
	AttemptsAllowed *int32
	AttemptsUsed    int64
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]SolutionAttempt, error) {
	rows, err := r.q.ListEventSolutionAttempts(ctx, postgres.ListEventSolutionAttemptsParams{
		EventID: f.EventID, TeamID: optionalUUID(f.TeamID), ParticipantID: optionalUUID(f.ParticipantID), ChallengeID: optionalUUID(f.ChallengeID),
		Correct: optionalBool(f.Correct), FromAt: optionalTime(f.FromAt), ToAt: optionalTime(f.ToAt),
		CursorReceivedAt: f.CursorReceivedAt, CursorID: f.CursorID, LimitVal: f.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SolutionAttempt, 0, len(rows))
	for _, row := range rows {
		item := SolutionAttempt{ID: row.ID, EventID: row.EventID, EventTeamID: row.EventTeamID, TeamName: row.TeamName, TeamChallengeID: row.TeamChallengeID, EventChallengeID: row.EventChallengeID, ChallengeName: row.ChallengeName, EventExerciseID: row.EventExerciseID, UserID: row.UserID, ParticipantName: row.ParticipantName, Answer: row.Answer, ExpectedFlag: row.ExpectedFlag, AutomaticCorrect: row.AutomaticCorrect, Correct: row.Correct, Decision: challengeAttempt.Decision(row.Decision), ReceivedAt: row.ReceivedAt}
		item.AttemptsAllowed, item.AttemptsUsed = limitState(row.AttemptsAllowed, row.AttemptsUsed, false).Max, row.AttemptsUsed
		if row.Scored {
			points := row.Points
			item.Points = &points
		}
		if row.LatestDecisionID != uuid.Nil {
			reason, decidedBy, decidedAt := row.DecisionReason, row.DecidedBy, row.DecidedAt
			item.DecisionReason, item.DecidedBy, item.DecidedAt = &reason, &decidedBy, &decidedAt
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *Repository) Count(ctx context.Context, f ListFilter) (int64, error) {
	return r.q.CountEventSolutionAttempts(ctx, postgres.CountEventSolutionAttemptsParams{EventID: f.EventID, TeamID: optionalUUID(f.TeamID), ParticipantID: optionalUUID(f.ParticipantID), ChallengeID: optionalUUID(f.ChallengeID), Correct: optionalBool(f.Correct), FromAt: optionalTime(f.FromAt), ToAt: optionalTime(f.ToAt)})
}

func (r *Repository) Cursor(ctx context.Context, eventID, id uuid.UUID) (time.Time, uuid.UUID, error) {
	row, err := r.q.GetEventSolutionAttemptCursor(ctx, postgres.GetEventSolutionAttemptCursorParams{ID: id, EventID: eventID})
	return row.ReceivedAt, row.ID, err
}

type DecisionTarget struct {
	ID, EventID, EventTeamID, TeamChallengeID, EventChallengeID uuid.UUID
	AutomaticCorrect                                            bool
	ReceivedAt                                                  time.Time
}

func (r *Repository) GetForDecision(ctx context.Context, eventID, attemptID uuid.UUID) (DecisionTarget, error) {
	row, err := r.q.GetEventSolutionAttemptForDecision(ctx, postgres.GetEventSolutionAttemptForDecisionParams{ID: attemptID, EventID: eventID})
	if err != nil {
		return DecisionTarget{}, err
	}
	return DecisionTarget{ID: row.ID, EventID: row.EventID, EventTeamID: row.EventTeamID, TeamChallengeID: row.TeamChallengeID, EventChallengeID: row.EventChallengeID, AutomaticCorrect: row.Correct, ReceivedAt: row.ReceivedAt}, nil
}

func (r *Repository) RecordDecision(ctx context.Context, value challengeAttempt.ManualDecision) error {
	_, err := r.q.CreateChallengeAttemptDecision(ctx, postgres.CreateChallengeAttemptDecisionParams{ID: value.ID, ChallengeAttemptID: value.AttemptID, Decision: int16(value.Decision), Reason: value.Reason, DecidedBy: value.DecidedBy, DecidedAt: value.DecidedAt})
	return err
}

// RefreshSolvedProjection rebuilds the solved cache from immutable attempts.
// awardedPoints is supplied only on the first fixed-mode solve; an existing
// award is never overwritten when moderation recalculates effective attempts.
func (r *Repository) RefreshSolvedProjection(ctx context.Context, teamChallengeID uuid.UUID, awardedPoints *int32) (time.Time, bool, error) {
	value, err := r.EffectiveSolvedAt(ctx, teamChallengeID)
	if err != nil {
		return time.Time{}, false, err
	}
	return r.RefreshSolvedProjectionValue(ctx, teamChallengeID, value, awardedPoints)
}

func (r *Repository) RefreshSolvedProjectionValue(ctx context.Context, teamChallengeID uuid.UUID, value postgres.GetEffectiveTeamChallengeSolvedAtRow, awardedPoints *int32) (time.Time, bool, error) {
	if !value.Solved {
		if err := r.q.DeleteTeamChallengeSolve(ctx, teamChallengeID); err != nil {
			return time.Time{}, false, err
		}
		return time.Time{}, false, nil
	}
	params := postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: teamChallengeID, SolvedAt: value.SolvedAt}
	if awardedPoints != nil {
		params.AwardedPoints = pgtype.Int4{Int32: *awardedPoints, Valid: true}
	}
	if err := r.q.UpsertTeamChallengeSolve(ctx, params); err != nil {
		return time.Time{}, false, err
	}
	return value.SolvedAt, true, nil
}

func (r *Repository) EffectiveSolvedAt(ctx context.Context, teamChallengeID uuid.UUID) (postgres.GetEffectiveTeamChallengeSolvedAtRow, error) {
	return r.q.GetEffectiveTeamChallengeSolvedAt(ctx, teamChallengeID)
}

func optionalUUID(value *uuid.UUID) uuid.NullUUID {
	if value == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *value, Valid: true}
}
func optionalBool(value *bool) pgtype.Bool {
	if value == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *value, Valid: true}
}
func optionalTime(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *value, Valid: true}
}

func ToDomain(row postgres.ChallengeAttempt) challengeAttempt.Attempt {
	return challengeAttempt.Attempt{ID: row.ID, EventID: row.EventID, EventTeamID: row.EventTeamID, TeamChallengeID: row.TeamChallengeID, UserID: row.UserID, Answer: row.Answer, Correct: row.Correct, ReceivedAt: row.ReceivedAt, CreatedAt: row.CreatedAt, Practice: row.Practice}
}
