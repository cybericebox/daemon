// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0
// source: exercise_categories.sql

package postgres

import (
	"context"

	"github.com/gofrs/uuid"
)

const createExerciseCategory = `-- name: CreateExerciseCategory :exec
insert into exercise_categories
    (id, name, description)
values ($1, $2, $3)
`

type CreateExerciseCategoryParams struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
}

func (q *Queries) CreateExerciseCategory(ctx context.Context, arg CreateExerciseCategoryParams) error {
	_, err := q.exec(ctx, q.createExerciseCategoryStmt, createExerciseCategory, arg.ID, arg.Name, arg.Description)
	return err
}

const deleteExerciseCategory = `-- name: DeleteExerciseCategory :exec
delete
from exercise_categories
where id = $1
`

func (q *Queries) DeleteExerciseCategory(ctx context.Context, id uuid.UUID) error {
	_, err := q.exec(ctx, q.deleteExerciseCategoryStmt, deleteExerciseCategory, id)
	return err
}

const getExerciseCategories = `-- name: GetExerciseCategories :many
select id, name, description, updated_at, updated_by, created_at
from exercise_categories
`

func (q *Queries) GetExerciseCategories(ctx context.Context) ([]ExerciseCategory, error) {
	rows, err := q.query(ctx, q.getExerciseCategoriesStmt, getExerciseCategories)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ExerciseCategory{}
	for rows.Next() {
		var i ExerciseCategory
		if err := rows.Scan(
			&i.ID,
			&i.Name,
			&i.Description,
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

const updateExerciseCategory = `-- name: UpdateExerciseCategory :exec
update exercise_categories
set name        = $2,
    description = $3
where id = $1
`

type UpdateExerciseCategoryParams struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
}

func (q *Queries) UpdateExerciseCategory(ctx context.Context, arg UpdateExerciseCategoryParams) error {
	_, err := q.exec(ctx, q.updateExerciseCategoryStmt, updateExerciseCategory, arg.ID, arg.Name, arg.Description)
	return err
}
