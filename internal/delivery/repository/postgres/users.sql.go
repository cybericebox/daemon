// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0
// source: users.sql

package postgres

import (
	"context"
	"database/sql"
	"time"

	"github.com/gofrs/uuid"
)

const createUser = `-- name: CreateUser :exec
insert into users (id, google_id, email, name, hashed_password, picture, role)
values ($1, $2, $3, $4, $5, $6, $7)
`

type CreateUserParams struct {
	ID             uuid.UUID      `json:"id"`
	GoogleID       sql.NullString `json:"google_id"`
	Email          string         `json:"email"`
	Name           string         `json:"name"`
	HashedPassword string         `json:"hashed_password"`
	Picture        string         `json:"picture"`
	Role           string         `json:"role"`
}

func (q *Queries) CreateUser(ctx context.Context, arg CreateUserParams) error {
	_, err := q.exec(ctx, q.createUserStmt, createUser,
		arg.ID,
		arg.GoogleID,
		arg.Email,
		arg.Name,
		arg.HashedPassword,
		arg.Picture,
		arg.Role,
	)
	return err
}

const deleteUser = `-- name: DeleteUser :exec
delete
from users
where id = $1
`

func (q *Queries) DeleteUser(ctx context.Context, id uuid.UUID) error {
	_, err := q.exec(ctx, q.deleteUserStmt, deleteUser, id)
	return err
}

const doesUserExistByID = `-- name: DoesUserExistByID :one
select exists(select 1 from users where id = $1) as exists
`

func (q *Queries) DoesUserExistByID(ctx context.Context, id uuid.UUID) (bool, error) {
	row := q.queryRow(ctx, q.doesUserExistByIDStmt, doesUserExistByID, id)
	var exists bool
	err := row.Scan(&exists)
	return exists, err
}

const getAllUsers = `-- name: GetAllUsers :many
select id, google_id, email, name, picture, role, last_seen, updated_at, updated_by, created_at
from users
order by name
`

type GetAllUsersRow struct {
	ID        uuid.UUID      `json:"id"`
	GoogleID  sql.NullString `json:"google_id"`
	Email     string         `json:"email"`
	Name      string         `json:"name"`
	Picture   string         `json:"picture"`
	Role      string         `json:"role"`
	LastSeen  time.Time      `json:"last_seen"`
	UpdatedAt sql.NullTime   `json:"updated_at"`
	UpdatedBy uuid.NullUUID  `json:"updated_by"`
	CreatedAt time.Time      `json:"created_at"`
}

func (q *Queries) GetAllUsers(ctx context.Context) ([]GetAllUsersRow, error) {
	rows, err := q.query(ctx, q.getAllUsersStmt, getAllUsers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GetAllUsersRow{}
	for rows.Next() {
		var i GetAllUsersRow
		if err := rows.Scan(
			&i.ID,
			&i.GoogleID,
			&i.Email,
			&i.Name,
			&i.Picture,
			&i.Role,
			&i.LastSeen,
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

const getUserByEmail = `-- name: GetUserByEmail :one
select id, google_id, email, name, hashed_password, picture, role, last_seen, updated_at, updated_by, created_at
from users
where email = $1
`

func (q *Queries) GetUserByEmail(ctx context.Context, email string) (User, error) {
	row := q.queryRow(ctx, q.getUserByEmailStmt, getUserByEmail, email)
	var i User
	err := row.Scan(
		&i.ID,
		&i.GoogleID,
		&i.Email,
		&i.Name,
		&i.HashedPassword,
		&i.Picture,
		&i.Role,
		&i.LastSeen,
		&i.UpdatedAt,
		&i.UpdatedBy,
		&i.CreatedAt,
	)
	return i, err
}

const getUserByID = `-- name: GetUserByID :one
select id, google_id, email, name, hashed_password, picture, role, last_seen, updated_at, updated_by, created_at
from users
where id = $1
`

func (q *Queries) GetUserByID(ctx context.Context, id uuid.UUID) (User, error) {
	row := q.queryRow(ctx, q.getUserByIDStmt, getUserByID, id)
	var i User
	err := row.Scan(
		&i.ID,
		&i.GoogleID,
		&i.Email,
		&i.Name,
		&i.HashedPassword,
		&i.Picture,
		&i.Role,
		&i.LastSeen,
		&i.UpdatedAt,
		&i.UpdatedBy,
		&i.CreatedAt,
	)
	return i, err
}

const getUsersWithSimilar = `-- name: GetUsersWithSimilar :many
select id, google_id, email, name, picture, role, last_seen, updated_at, updated_by, created_at
from users
where name ilike '%' || $1::text || '%'
   or email ilike '%' || $1 || '%'
order by name
`

type GetUsersWithSimilarRow struct {
	ID        uuid.UUID      `json:"id"`
	GoogleID  sql.NullString `json:"google_id"`
	Email     string         `json:"email"`
	Name      string         `json:"name"`
	Picture   string         `json:"picture"`
	Role      string         `json:"role"`
	LastSeen  time.Time      `json:"last_seen"`
	UpdatedAt sql.NullTime   `json:"updated_at"`
	UpdatedBy uuid.NullUUID  `json:"updated_by"`
	CreatedAt time.Time      `json:"created_at"`
}

func (q *Queries) GetUsersWithSimilar(ctx context.Context, search string) ([]GetUsersWithSimilarRow, error) {
	rows, err := q.query(ctx, q.getUsersWithSimilarStmt, getUsersWithSimilar, search)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []GetUsersWithSimilarRow{}
	for rows.Next() {
		var i GetUsersWithSimilarRow
		if err := rows.Scan(
			&i.ID,
			&i.GoogleID,
			&i.Email,
			&i.Name,
			&i.Picture,
			&i.Role,
			&i.LastSeen,
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

const setLastSeen = `-- name: SetLastSeen :exec
update users
set last_seen = now()
where id = $1
`

func (q *Queries) SetLastSeen(ctx context.Context, id uuid.UUID) error {
	_, err := q.exec(ctx, q.setLastSeenStmt, setLastSeen, id)
	return err
}

const updateUserEmail = `-- name: UpdateUserEmail :exec
update users
set email      = $2,
    updated_at = now()
where id = $1
`

type UpdateUserEmailParams struct {
	ID    uuid.UUID `json:"id"`
	Email string    `json:"email"`
}

func (q *Queries) UpdateUserEmail(ctx context.Context, arg UpdateUserEmailParams) error {
	_, err := q.exec(ctx, q.updateUserEmailStmt, updateUserEmail, arg.ID, arg.Email)
	return err
}

const updateUserGoogleID = `-- name: UpdateUserGoogleID :exec
update users
set google_id  = $2,
    updated_at = now()
where id = $1
`

type UpdateUserGoogleIDParams struct {
	ID       uuid.UUID      `json:"id"`
	GoogleID sql.NullString `json:"google_id"`
}

func (q *Queries) UpdateUserGoogleID(ctx context.Context, arg UpdateUserGoogleIDParams) error {
	_, err := q.exec(ctx, q.updateUserGoogleIDStmt, updateUserGoogleID, arg.ID, arg.GoogleID)
	return err
}

const updateUserName = `-- name: UpdateUserName :exec
update users
set name       = $2,
    updated_at = now()
where id = $1
`

type UpdateUserNameParams struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

func (q *Queries) UpdateUserName(ctx context.Context, arg UpdateUserNameParams) error {
	_, err := q.exec(ctx, q.updateUserNameStmt, updateUserName, arg.ID, arg.Name)
	return err
}

const updateUserPassword = `-- name: UpdateUserPassword :exec
update users
set hashed_password = $2,
    updated_at      = now()
where id = $1
`

type UpdateUserPasswordParams struct {
	ID             uuid.UUID `json:"id"`
	HashedPassword string    `json:"hashed_password"`
}

func (q *Queries) UpdateUserPassword(ctx context.Context, arg UpdateUserPasswordParams) error {
	_, err := q.exec(ctx, q.updateUserPasswordStmt, updateUserPassword, arg.ID, arg.HashedPassword)
	return err
}

const updateUserPicture = `-- name: UpdateUserPicture :exec
update users
set picture    = $2,
    updated_at = now()
where id = $1
`

type UpdateUserPictureParams struct {
	ID      uuid.UUID `json:"id"`
	Picture string    `json:"picture"`
}

func (q *Queries) UpdateUserPicture(ctx context.Context, arg UpdateUserPictureParams) error {
	_, err := q.exec(ctx, q.updateUserPictureStmt, updateUserPicture, arg.ID, arg.Picture)
	return err
}

const updateUserRole = `-- name: UpdateUserRole :exec
update users
set role       = $2,
    updated_at = now()
where id = $1
`

type UpdateUserRoleParams struct {
	ID   uuid.UUID `json:"id"`
	Role string    `json:"role"`
}

func (q *Queries) UpdateUserRole(ctx context.Context, arg UpdateUserRoleParams) error {
	_, err := q.exec(ctx, q.updateUserRoleStmt, updateUserRole, arg.ID, arg.Role)
	return err
}
