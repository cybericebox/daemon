package eventAnalyticsRepo

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// IntegrityQueries are the statements of «Доброчесність» (§6.6); Queries
// embeds it.
type IntegrityQueries interface {
	ListEventIntegrityAttempts(context.Context, uuid.UUID) ([]postgres.ListEventIntegrityAttemptsRow, error)
	ListEventIntegrityRejections(context.Context, uuid.UUID) ([]postgres.ListEventIntegrityRejectionsRow, error)
	ListEventIntegritySolves(context.Context, uuid.UUID) ([]postgres.ListEventIntegritySolvesRow, error)
	ListEventIntegrityCrossFlags(context.Context, uuid.UUID) ([]postgres.ListEventIntegrityCrossFlagsRow, error)
	GetEventIntegrityCollectorStart(context.Context, uuid.UUID) (time.Time, error)
	GetIntegrityTaskOfTeamChallenge(context.Context, postgres.GetIntegrityTaskOfTeamChallengeParams) (postgres.GetIntegrityTaskOfTeamChallengeRow, error)
	ListIntegrityDismissals(context.Context, uuid.UUID) ([]postgres.ListIntegrityDismissalsRow, error)
	CreateIntegrityDismissal(context.Context, postgres.CreateIntegrityDismissalParams) (int64, error)
	DeleteIntegrityDismissal(context.Context, postgres.DeleteIntegrityDismissalParams) (int64, error)
	ListSolveIntegrityReviews(context.Context, uuid.UUID) ([]postgres.ListSolveIntegrityReviewsRow, error)
	UpsertSolveIntegrityReview(context.Context, postgres.UpsertSolveIntegrityReviewParams) (uuid.UUID, error)
	DeleteSolveIntegrityReview(context.Context, postgres.DeleteSolveIntegrityReviewParams) (int64, error)
}

// SolveReview is an organizer's «перевірено» note on one solve.
type SolveReview struct {
	TeamChallengeID uuid.UUID
	Note            string
	ReviewedBy      uuid.UUID
	ReviewedByName  string
	ReviewedAt      time.Time
}

// IntegrityDismissal is a stored «не підсвічувати такі випадки».
type IntegrityDismissal struct {
	ID            uuid.UUID
	Scope         eventAnalyticsModel.DismissScope
	ExerciseID    uuid.UUID
	TaskID        uuid.UUID
	Kind          eventAnalyticsModel.IntegrityKind
	Key           string
	Note          string
	CreatedByName string
	CreatedAt     time.Time
	ChallengeName string
}

// noMoment maps the epoch sentinel of the SQL («never happened») to nil.
func noMoment(t time.Time) *time.Time {
	if t.IsZero() || t.Unix() <= 0 {
		return nil
	}
	return &t
}

// IntegrityFacts reads what the integrity detectors work on: every solve,
// attempt and rate-limit style rejection of the teams visible in the results
// (not hidden, not the moderators team), with the access evidence of each
// solve.
func (r *Repository) IntegrityFacts(ctx context.Context, eventID uuid.UUID) (eventAnalyticsModel.IntegrityFacts, error) {
	solves, err := r.q.ListEventIntegritySolves(ctx, eventID)
	if err != nil {
		return eventAnalyticsModel.IntegrityFacts{}, err
	}
	attempts, err := r.q.ListEventIntegrityAttempts(ctx, eventID)
	if err != nil {
		return eventAnalyticsModel.IntegrityFacts{}, err
	}
	rejections, err := r.q.ListEventIntegrityRejections(ctx, eventID)
	if err != nil {
		return eventAnalyticsModel.IntegrityFacts{}, err
	}
	crossFlags, err := r.q.ListEventIntegrityCrossFlags(ctx, eventID)
	if err != nil {
		return eventAnalyticsModel.IntegrityFacts{}, err
	}
	start, err := r.q.GetEventIntegrityCollectorStart(ctx, eventID)
	if err != nil {
		return eventAnalyticsModel.IntegrityFacts{}, err
	}
	facts := eventAnalyticsModel.IntegrityFacts{
		Solves:         make([]eventAnalyticsModel.IntegritySolve, 0, len(solves)),
		Attempts:       make([]eventAnalyticsModel.IntegrityAttempt, 0, len(attempts)),
		Rejections:     make([]eventAnalyticsModel.IntegrityRejection, 0, len(rejections)),
		CollectorStart: noMoment(start),
		CrossFlags:     make([]eventAnalyticsModel.IntegrityCrossSubmission, 0, len(crossFlags)),
	}
	for _, x := range crossFlags {
		facts.CrossFlags = append(facts.CrossFlags, eventAnalyticsModel.IntegrityCrossSubmission{
			TeamChallengeID: x.TeamChallengeID, TeamID: x.TeamID, TeamName: x.TeamName,
			ChallengeID: x.ChallengeID, ChallengeName: x.ChallengeName, ExerciseID: x.ExerciseID, TaskID: x.TaskID,
			Level: x.Level, At: x.At, OwnerTeam: eventAnalyticsModel.IntegrityTeam{ID: x.OwnerTeamID, Name: x.OwnerTeamName},
			OwnerChallenge: x.OwnerChallengeID, OwnerName: x.OwnerChallengeName,
		})
	}
	for _, s := range solves {
		flag := eventAnalyticsModel.FlagUnknown
		switch s.StaticFlag {
		case 1:
			flag = eventAnalyticsModel.FlagStatic
		case 0:
			flag = eventAnalyticsModel.FlagDynamic
		}
		facts.Solves = append(facts.Solves, eventAnalyticsModel.IntegritySolve{
			TeamChallengeID: s.TeamChallengeID, TeamID: s.TeamID, TeamName: s.TeamName,
			ChallengeID: s.ChallengeID, ChallengeName: s.ChallengeName, ExerciseID: s.ExerciseID, TaskID: s.TaskID, Level: s.Level,
			AttachmentCount: int(s.AttachmentCount), SolvedAt: s.SolvedAt, HasLab: s.HasLab, VPNAccess: s.VpnAccess, ProxyAccess: s.ProxyAccess, Flag: flag,
			FirstOpen: noMoment(s.FirstOpenAt), FirstFile: noMoment(s.FirstDownloadAt),
			FirstHint: noMoment(s.FirstHintAt), FirstVPN: noMoment(s.FirstVpnAt),
		})
	}
	for _, a := range attempts {
		facts.Attempts = append(facts.Attempts, eventAnalyticsModel.IntegrityAttempt{
			TeamChallengeID: a.TeamChallengeID, TeamID: a.TeamID, ChallengeID: a.ChallengeID,
			Answer: a.Answer, Correct: a.Correct, At: a.ReceivedAt,
		})
	}
	for _, x := range rejections {
		facts.Rejections = append(facts.Rejections, eventAnalyticsModel.IntegrityRejection{
			TeamID: x.TeamID, ChallengeID: x.ChallengeID, Reason: x.Reason, At: x.At,
		})
	}
	return facts, nil
}

// SolveReviews reads the organizers' notes of the event's solves.
func (r *Repository) SolveReviews(ctx context.Context, eventID uuid.UUID) ([]SolveReview, error) {
	rows, err := r.q.ListSolveIntegrityReviews(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]SolveReview, 0, len(rows))
	for _, row := range rows {
		out = append(out, SolveReview{
			TeamChallengeID: row.TeamChallengeID, Note: row.Note, ReviewedBy: row.ReviewedBy.UUID,
			ReviewedByName: row.ReviewedByName, ReviewedAt: row.ReviewedAt,
		})
	}
	return out, nil
}

// SaveSolveReview stores (or replaces) the note of a solve of the event; false
// when the solve is not one of the event.
func (r *Repository) SaveSolveReview(ctx context.Context, eventID, teamChallengeID, reviewer uuid.UUID, note string, at time.Time) (bool, error) {
	_, err := r.q.UpsertSolveIntegrityReview(ctx, postgres.UpsertSolveIntegrityReviewParams{
		Note: note, ReviewedBy: uuid.NullUUID{UUID: reviewer, Valid: reviewer != uuid.Nil}, ReviewedAt: at,
		TeamChallengeID: teamChallengeID, EventID: eventID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// DeleteSolveReview removes the note; false when there was none.
func (r *Repository) DeleteSolveReview(ctx context.Context, eventID, teamChallengeID uuid.UUID) (bool, error) {
	n, err := r.q.DeleteSolveIntegrityReview(ctx, postgres.DeleteSolveIntegrityReviewParams{TeamChallengeID: teamChallengeID, EventID: eventID})
	return n > 0, err
}

// Dismissals reads the dismissals that apply to the event: its own and those
// of the catalog exercises it uses.
func (r *Repository) Dismissals(ctx context.Context, eventID uuid.UUID) ([]IntegrityDismissal, error) {
	rows, err := r.q.ListIntegrityDismissals(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]IntegrityDismissal, 0, len(rows))
	for _, row := range rows {
		out = append(out, IntegrityDismissal{
			ID: row.ID, Scope: eventAnalyticsModel.DismissScope(row.Scope), ExerciseID: row.ExerciseID, TaskID: row.TaskID,
			Kind: eventAnalyticsModel.IntegrityKind(row.Kind), Key: row.Key, Note: row.Note,
			CreatedByName: row.CreatedByName, CreatedAt: row.CreatedAt, ChallengeName: row.ChallengeName,
		})
	}
	return out, nil
}

// SaveDismissal stores a dismissal for the catalog task behind a team task of
// the event; false when the team task is not one of the event. Saving the
// same dismissal twice is a no-op.
func (r *Repository) SaveDismissal(ctx context.Context, eventID, teamChallengeID uuid.UUID, scope eventAnalyticsModel.DismissScope,
	kind eventAnalyticsModel.IntegrityKind, key, note string, by uuid.UUID, id uuid.UUID, at time.Time) (bool, error) {
	task, err := r.q.GetIntegrityTaskOfTeamChallenge(ctx, postgres.GetIntegrityTaskOfTeamChallengeParams{TeamChallengeID: teamChallengeID, EventID: eventID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	params := postgres.CreateIntegrityDismissalParams{
		ID: id, Scope: string(scope), ExerciseID: task.ExerciseID, TaskID: task.TaskID, Kind: string(kind), Key: key, Note: note,
		CreatedBy: uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}, CreatedAt: at,
	}
	if scope == eventAnalyticsModel.DismissEvent {
		params.EventID = uuid.NullUUID{UUID: eventID, Valid: true}
	}
	if _, err = r.q.CreateIntegrityDismissal(ctx, params); err != nil {
		return false, err
	}
	return true, nil
}

// DeleteDismissal removes a dismissal that applies to the event; false when
// there is none.
func (r *Repository) DeleteDismissal(ctx context.Context, eventID, id uuid.UUID) (bool, error) {
	n, err := r.q.DeleteIntegrityDismissal(ctx, postgres.DeleteIntegrityDismissalParams{ID: id, EventID: uuid.NullUUID{UUID: eventID, Valid: true}})
	return n > 0, err
}
