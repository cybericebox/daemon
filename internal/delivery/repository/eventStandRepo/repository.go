// Package eventStandRepo persists team stands and the event stand rollout.
// Narrow-query exceptions, all set operations or engine read shapes: the
// stand status write is conditional on the previously read status (so a
// failure transition is observed exactly once), and the moderators team is
// created by an insert-select that finds the event owner in the same statement.
package eventStandRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

type Queries interface {
	ListStandEvents(ctx context.Context, now time.Time) ([]uuid.UUID, error)
	GetEventStandRollout(ctx context.Context, eventID uuid.UUID) (postgres.EventStandRollout, error)
	OpenEventStandRollout(ctx context.Context, arg postgres.OpenEventStandRolloutParams) error
	TearDownEventStandRollout(ctx context.Context, arg postgres.TearDownEventStandRolloutParams) error
	CreateModeratorsTeam(ctx context.Context, arg postgres.CreateModeratorsTeamParams) error
	GetModeratorsTeam(ctx context.Context, eventID uuid.UUID) (postgres.EventTeam, error)
	GetStandTeam(ctx context.Context, arg postgres.GetStandTeamParams) (postgres.EventTeam, error)
	ListMissingTeamAssignments(ctx context.Context, arg postgres.ListMissingTeamAssignmentsParams) ([]postgres.ListMissingTeamAssignmentsRow, error)
	ListStandTeams(ctx context.Context, eventID uuid.UUID) ([]postgres.ListStandTeamsRow, error)
	CreateEventTeamStand(ctx context.Context, arg postgres.CreateEventTeamStandParams) (int64, error)
	UpdateEventTeamStand(ctx context.Context, arg postgres.UpdateEventTeamStandParams) (int64, error)
	RemoveEventTeamStands(ctx context.Context, arg postgres.RemoveEventTeamStandsParams) error
	ListEventTeamLabGroups(ctx context.Context, eventID uuid.UUID) ([]string, error)
	ListEventStandLabs(ctx context.Context, eventID uuid.UUID) ([]postgres.ListEventStandLabsRow, error)
	ListModeratorsTeamChallenges(ctx context.Context, eventID uuid.UUID) ([]postgres.ListModeratorsTeamChallengesRow, error)
	ListEventStandRecipients(ctx context.Context, eventID uuid.UUID) ([]uuid.UUID, error)
	GetEventTeamStand(ctx context.Context, eventTeamID uuid.UUID) (postgres.EventTeamStand, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// Rollout is the event-level stand state. A missing row reads as zero.
type Rollout struct {
	OpenedAt   *time.Time
	TornDownAt *time.Time
}

// Team is one stand candidate with its persisted stand and Lab counters.
type Team struct {
	TeamID     uuid.UUID
	PublicName string
	Individual bool
	Moderators bool
	Admitted   bool
	HasStand   bool
	Status     eventStandModel.Status
	Reason     string
	Generation int32
	UpdatedAt  *time.Time
	Counters   eventStandModel.LabCounters
	// LabGeneration is the highest Lab generation of the team.
	LabGeneration int32
}

// Lab is one team Lab in the moderator stand view.
type Lab struct {
	TeamID        uuid.UUID
	ChallengeID   uuid.UUID
	ChallengeName string
	Readiness     labBindingModel.Readiness
	FailureReason string
}

// Assignment is one (team, active exercise) pair still to be prepared.
type Assignment struct {
	TeamID          uuid.UUID
	EventExerciseID uuid.UUID
}

// ModeratorsChallenge is one assignment of the moderators team.
type ModeratorsChallenge struct {
	ChallengeID  uuid.UUID
	Name         string
	Readiness    teamChallengeModel.Readiness
	LabReadiness *labBindingModel.Readiness
}

func (r *Repository) ListEvents(ctx context.Context, now time.Time) ([]uuid.UUID, error) {
	return r.q.ListStandEvents(ctx, now)
}

func (r *Repository) GetRollout(ctx context.Context, eventID uuid.UUID) (Rollout, error) {
	row, err := r.q.GetEventStandRollout(ctx, eventID)
	if err != nil {
		return Rollout{}, err
	}
	return Rollout{OpenedAt: timePtr(row.OpenedAt), TornDownAt: timePtr(row.TornDownAt)}, nil
}

func (r *Repository) OpenRollout(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	return r.q.OpenEventStandRollout(ctx, postgres.OpenEventStandRolloutParams{EventID: eventID, Now: now})
}

func (r *Repository) TearDownRollout(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	return r.q.TearDownEventStandRollout(ctx, postgres.TearDownEventStandRolloutParams{EventID: eventID, Now: now})
}

// EnsureModeratorsTeam creates the hidden team once; a repeat is a no-op.
func (r *Repository) EnsureModeratorsTeam(ctx context.Context, eventID, teamID uuid.UUID, joinCode string, now time.Time) error {
	return r.q.CreateModeratorsTeam(ctx, postgres.CreateModeratorsTeamParams{
		ID: teamID, EventID: eventID, Name: eventStandModel.ModeratorsTeamName(eventID), JoinCode: joinCode, Now: now,
	})
}

func (r *Repository) GetModeratorsTeam(ctx context.Context, eventID uuid.UUID) (eventTeamModel.EventTeam, error) {
	row, err := r.q.GetModeratorsTeam(ctx, eventID)
	if err != nil {
		return eventTeamModel.EventTeam{}, err
	}
	return eventTeamRepo.ToDomain(row), nil
}

// GetTeam reads any team of the event, the moderators team included.
func (r *Repository) GetTeam(ctx context.Context, eventID, teamID uuid.UUID) (eventTeamModel.EventTeam, error) {
	row, err := r.q.GetStandTeam(ctx, postgres.GetStandTeamParams{ID: teamID, EventID: eventID})
	if err != nil {
		return eventTeamModel.EventTeam{}, err
	}
	return eventTeamRepo.ToDomain(row), nil
}

func (r *Repository) ListMissingAssignments(ctx context.Context, eventID uuid.UUID, infrastructureAllowed bool) ([]Assignment, error) {
	rows, err := r.q.ListMissingTeamAssignments(ctx, postgres.ListMissingTeamAssignmentsParams{EventID: eventID, InfrastructureAllowed: infrastructureAllowed})
	if err != nil {
		return nil, err
	}
	out := make([]Assignment, 0, len(rows))
	for _, row := range rows {
		out = append(out, Assignment{TeamID: row.EventTeamID, EventExerciseID: row.EventExerciseID})
	}
	return out, nil
}

func (r *Repository) ListTeams(ctx context.Context, eventID uuid.UUID) ([]Team, error) {
	rows, err := r.q.ListStandTeams(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]Team, 0, len(rows))
	for _, row := range rows {
		team := Team{
			TeamID: row.ID, PublicName: row.PublicName, Individual: row.Individual, Moderators: row.Moderators,
			Admitted: row.Admitted, HasStand: row.StandStatus.Valid,
			Counters: eventStandModel.LabCounters{
				PendingLabs: row.PendingLabs, FailedLabs: row.FailedLabs,
				FailureReason: row.FailureReason, MissingAssignments: row.MissingAssignments,
			},
			LabGeneration: row.LabGeneration,
		}
		if row.StandStatus.Valid {
			team.Status = eventStandModel.Status(row.StandStatus.Int16)
			team.Reason = row.StandReason.String
			team.Generation = row.StandGeneration.Int32
			team.UpdatedAt = timePtr(row.StandUpdatedAt)
		}
		out = append(out, team)
	}
	return out, nil
}

// CreateStand inserts the first status of a team stand; false when a
// concurrent pass created it first.
func (r *Repository) CreateStand(ctx context.Context, eventID, teamID uuid.UUID, status eventStandModel.Status, reason string, generation int32, now time.Time) (bool, error) {
	affected, err := r.q.CreateEventTeamStand(ctx, postgres.CreateEventTeamStandParams{
		EventTeamID: teamID, EventID: eventID, Status: int16(status), Reason: optionalText(reason), Generation: generation, Now: now,
	})
	return affected == 1, err
}

// UpdateStand changes a stand only from the status the caller read; false
// means another writer changed it first (or the stand was removed).
func (r *Repository) UpdateStand(ctx context.Context, teamID uuid.UUID, expected, status eventStandModel.Status, reason string, generation int32, now time.Time) (bool, error) {
	affected, err := r.q.UpdateEventTeamStand(ctx, postgres.UpdateEventTeamStandParams{
		EventTeamID: teamID, ExpectedStatus: int16(expected), Status: int16(status), Reason: optionalText(reason), Generation: generation, Now: now,
	})
	return affected == 1, err
}

// GetStatus returns a team's stand status; no stand reads as not deployed.
func (r *Repository) GetStatus(ctx context.Context, teamID uuid.UUID) (eventStandModel.Status, error) {
	row, err := r.q.GetEventTeamStand(ctx, teamID)
	if err != nil {
		return eventStandModel.StatusNotDeployed, err
	}
	return eventStandModel.Status(row.Status), nil
}

func (r *Repository) RemoveStands(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	return r.q.RemoveEventTeamStands(ctx, postgres.RemoveEventTeamStandsParams{EventID: eventID, Now: now})
}

func (r *Repository) ListLabGroups(ctx context.Context, eventID uuid.UUID) ([]string, error) {
	return r.q.ListEventTeamLabGroups(ctx, eventID)
}

func (r *Repository) ListLabs(ctx context.Context, eventID uuid.UUID) ([]Lab, error) {
	rows, err := r.q.ListEventStandLabs(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]Lab, 0, len(rows))
	for _, row := range rows {
		out = append(out, Lab{TeamID: row.EventTeamID, ChallengeID: row.EventChallengeID, ChallengeName: row.ChallengeName,
			Readiness: labBindingModel.Readiness(row.Readiness), FailureReason: row.FailureReason.String})
	}
	return out, nil
}

func (r *Repository) ListModeratorsChallenges(ctx context.Context, eventID uuid.UUID) ([]ModeratorsChallenge, error) {
	rows, err := r.q.ListModeratorsTeamChallenges(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]ModeratorsChallenge, 0, len(rows))
	for _, row := range rows {
		item := ModeratorsChallenge{ChallengeID: row.EventChallengeID, Name: row.ChallengeName, Readiness: teamChallengeModel.Readiness(row.Readiness)}
		if row.LabReadiness.Valid {
			readiness := labBindingModel.Readiness(row.LabReadiness.Int16)
			item.LabReadiness = &readiness
		}
		out = append(out, item)
	}
	return out, nil
}

func (r *Repository) ListRecipients(ctx context.Context, eventID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.ListEventStandRecipients(ctx, eventID)
}

func timePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time
	return &at
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}
