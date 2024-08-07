// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0
// source: event_teams.sql

package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/gofrs/uuid"
)

const countTeamsInEvents = `-- name: CountTeamsInEvents :many
select count(*), event_id
from event_teams
group by event_id
`

type CountTeamsInEventsRow struct {
	Count   int64     `json:"count"`
	EventID uuid.UUID `json:"event_id"`
}

func (q *Queries) CountTeamsInEvents(ctx context.Context) ([]CountTeamsInEventsRow, error) {
	rows, err := q.query(ctx, q.countTeamsInEventsStmt, countTeamsInEvents)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []CountTeamsInEventsRow{}
	for rows.Next() {
		var i CountTeamsInEventsRow
		if err := rows.Scan(&i.Count, &i.EventID); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const createTeamInEvent = `-- name: CreateTeamInEvent :exec
insert into event_teams (id, name, join_code, event_id, laboratory_id)
values ($1, $2, $3, $4, $5)
`

type CreateTeamInEventParams struct {
	ID           uuid.UUID     `json:"id"`
	Name         string        `json:"name"`
	JoinCode     string        `json:"join_code"`
	EventID      uuid.UUID     `json:"event_id"`
	LaboratoryID uuid.NullUUID `json:"laboratory_id"`
}

func (q *Queries) CreateTeamInEvent(ctx context.Context, arg CreateTeamInEventParams) error {
	_, err := q.exec(ctx, q.createTeamInEventStmt, createTeamInEvent,
		arg.ID,
		arg.Name,
		arg.JoinCode,
		arg.EventID,
		arg.LaboratoryID,
	)
	return err
}

const getEventParticipantTeam = `-- name: GetEventParticipantTeam :one
select event_teams.id, name, join_code, laboratory_id
from event_teams
         join event_participants on event_teams.id = event_participants.team_id
where event_participants.event_id = $1
  and event_participants.user_id = $2
`

type GetEventParticipantTeamParams struct {
	EventID uuid.UUID `json:"event_id"`
	UserID  uuid.UUID `json:"user_id"`
}

type GetEventParticipantTeamRow struct {
	ID           uuid.UUID     `json:"id"`
	Name         string        `json:"name"`
	JoinCode     string        `json:"join_code"`
	LaboratoryID uuid.NullUUID `json:"laboratory_id"`
}

func (q *Queries) GetEventParticipantTeam(ctx context.Context, arg GetEventParticipantTeamParams) (GetEventParticipantTeamRow, error) {
	row := q.queryRow(ctx, q.getEventParticipantTeamStmt, getEventParticipantTeam, arg.EventID, arg.UserID)
	var i GetEventParticipantTeamRow
	err := row.Scan(
		&i.ID,
		&i.Name,
		&i.JoinCode,
		&i.LaboratoryID,
	)
	return i, err
}

const getEventParticipantTeamID = `-- name: GetEventParticipantTeamID :one
select team_id
from event_participants
where event_id = $1
  and user_id = $2
`

type GetEventParticipantTeamIDParams struct {
	EventID uuid.UUID `json:"event_id"`
	UserID  uuid.UUID `json:"user_id"`
}

func (q *Queries) GetEventParticipantTeamID(ctx context.Context, arg GetEventParticipantTeamIDParams) (uuid.NullUUID, error) {
	row := q.queryRow(ctx, q.getEventParticipantTeamIDStmt, getEventParticipantTeamID, arg.EventID, arg.UserID)
	var team_id uuid.NullUUID
	err := row.Scan(&team_id)
	return team_id, err
}

const getEventTeamByName = `-- name: GetEventTeamByName :one
select id, name, join_code
from event_teams
where name = $1
  and event_id = $2
`

type GetEventTeamByNameParams struct {
	Name    string    `json:"name"`
	EventID uuid.UUID `json:"event_id"`
}

type GetEventTeamByNameRow struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	JoinCode string    `json:"join_code"`
}

func (q *Queries) GetEventTeamByName(ctx context.Context, arg GetEventTeamByNameParams) (GetEventTeamByNameRow, error) {
	row := q.queryRow(ctx, q.getEventTeamByNameStmt, getEventTeamByName, arg.Name, arg.EventID)
	var i GetEventTeamByNameRow
	err := row.Scan(&i.ID, &i.Name, &i.JoinCode)
	return i, err
}

const getEventTeams = `-- name: GetEventTeams :many
select id, event_id, name, laboratory_id, updated_at, updated_by, created_at
from event_teams
where event_id = $1
`

type GetEventTeamsRow struct {
	ID           uuid.UUID     `json:"id"`
	EventID      uuid.UUID     `json:"event_id"`
	Name         string        `json:"name"`
	LaboratoryID uuid.NullUUID `json:"laboratory_id"`
	UpdatedAt    sql.NullTime  `json:"updated_at"`
	UpdatedBy    uuid.NullUUID `json:"updated_by"`
	CreatedAt    time.Time     `json:"created_at"`
}

func (q *Queries) GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]GetEventTeamsRow, error) {
	rows, err := q.query(ctx, q.getEventTeamsStmt, getEventTeams, eventID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GetEventTeamsRow{}
	for rows.Next() {
		var i GetEventTeamsRow
		if err := rows.Scan(
			&i.ID,
			&i.EventID,
			&i.Name,
			&i.LaboratoryID,
			&i.UpdatedAt,
			&i.UpdatedBy,
			&i.CreatedAt,
		); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const teamExistsInEvent = `-- name: TeamExistsInEvent :one
select EXISTS(select true as exists from event_teams where name = $1 and event_id = $2) as exists
`

type TeamExistsInEventParams struct {
	Name    string    `json:"name"`
	EventID uuid.UUID `json:"event_id"`
}

func (q *Queries) TeamExistsInEvent(ctx context.Context, arg TeamExistsInEventParams) (bool, error) {
	row := q.queryRow(ctx, q.teamExistsInEventStmt, teamExistsInEvent, arg.Name, arg.EventID)
	var exists bool
	err := row.Scan(&exists)
	return exists, err
}
