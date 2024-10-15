// Code generated by sqlc. DO NOT EDIT.
// versions:
//   sqlc v1.25.0
// source: platform_settings.sql

package postgres

import (
	"context"

	"github.com/gofrs/uuid"
)

const getEmailTemplateBody = `-- name: GetEmailTemplateBody :one
select value
from platform_settings
where type = 'email_template_body'
  and key = $1
`

func (q *Queries) GetEmailTemplateBody(ctx context.Context, key string) (string, error) {
	row := q.db.QueryRow(ctx, getEmailTemplateBody, key)
	var value string
	err := row.Scan(&value)
	return value, err
}

const getEmailTemplateSubject = `-- name: GetEmailTemplateSubject :one
select value
from platform_settings
where type = 'email_template_subject'
  and key = $1
`

func (q *Queries) GetEmailTemplateSubject(ctx context.Context, key string) (string, error) {
	row := q.db.QueryRow(ctx, getEmailTemplateSubject, key)
	var value string
	err := row.Scan(&value)
	return value, err
}

const updateEmailTemplateBody = `-- name: UpdateEmailTemplateBody :execrows
update platform_settings
set value = $2,
    updated_at = now(),
    updated_by = $3
where type = 'email_template_body'
  and key = $1
`

type UpdateEmailTemplateBodyParams struct {
	Key       string        `json:"key"`
	Value     string        `json:"value"`
	UpdatedBy uuid.NullUUID `json:"updated_by"`
}

func (q *Queries) UpdateEmailTemplateBody(ctx context.Context, arg UpdateEmailTemplateBodyParams) (int64, error) {
	result, err := q.db.Exec(ctx, updateEmailTemplateBody, arg.Key, arg.Value, arg.UpdatedBy)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}

const updateEmailTemplateSubject = `-- name: UpdateEmailTemplateSubject :execrows
update platform_settings
set value = $2,
    updated_at = now(),
    updated_by = $3
where type = 'email_template_subject'
  and key = $1
`

type UpdateEmailTemplateSubjectParams struct {
	Key       string        `json:"key"`
	Value     string        `json:"value"`
	UpdatedBy uuid.NullUUID `json:"updated_by"`
}

func (q *Queries) UpdateEmailTemplateSubject(ctx context.Context, arg UpdateEmailTemplateSubjectParams) (int64, error) {
	result, err := q.db.Exec(ctx, updateEmailTemplateSubject, arg.Key, arg.Value, arg.UpdatedBy)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected(), nil
}
