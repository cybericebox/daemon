// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0
// source: files.sql

package postgres

import (
	"context"

	"github.com/gofrs/uuid"
)

const createFile = `-- name: CreateFile :exec
insert into files (id, storage_type)
values ($1, $2)
`

type CreateFileParams struct {
	ID          uuid.UUID `json:"id"`
	StorageType string    `json:"storage_type"`
}

func (q *Queries) CreateFile(ctx context.Context, arg CreateFileParams) error {
	_, err := q.db.Exec(ctx, createFile, arg.ID, arg.StorageType)
	return err
}

const getFiles = `-- name: GetFiles :many
select id, storage_type, created_at
from files
order by created_at
`

func (q *Queries) GetFiles(ctx context.Context) ([]File, error) {
	rows, err := q.db.Query(ctx, getFiles)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []File{}
	for rows.Next() {
		var i File
		if err := rows.Scan(&i.ID, &i.StorageType, &i.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, i)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
