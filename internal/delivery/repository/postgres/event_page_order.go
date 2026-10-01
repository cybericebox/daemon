package postgres

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
)

// PublishEventPageParams carries the draft values being published and the
// complete navbar order computed from the pages read with ExpectedUpdatedAt.
type PublishEventPageParams struct {
	ID                 uuid.UUID
	EventID            uuid.UUID
	ExpectedUpdatedAt  time.Time
	Slug               string
	Title              string
	Document           []byte
	Visibility         int16
	Navigation         int16
	PageIDs            []uuid.UUID
	ChallengesPosition int
	ResultsPosition    int
	Now                time.Time
}

// PublishEventPage promotes a page draft and rewrites the navbar order in one
// statement. The ordering keeps the three stored ranges: far negative before
// Challenges, negative before Results, nonnegative after Results. The whole
// statement is a no-op unless the page still has the draft that was read
// (updated_at guard); the published page count is returned.
func (q *Queries) PublishEventPage(ctx context.Context, arg PublishEventPageParams) (int64, error) {
	const query = `
WITH desired AS (
	SELECT id, CASE
		WHEN ordinality <= $10 THEN (ordinality - 1 - 1000000000)::integer
		ELSE (ordinality - 1 - $11)::integer
	END AS position
	FROM unnest($9::uuid[]) WITH ORDINALITY AS ordered(id, ordinality)
), published AS (
	UPDATE event_pages AS page
	SET slug = $4, title = $5, document = $6, visibility = $7, navigation = $8,
	    navigation_order = COALESCE((SELECT position FROM desired WHERE desired.id = page.id), page.navigation_order),
	    draft = NULL, published_at = $12, updated_at = $12
	WHERE page.id = $1 AND page.event_id = $2 AND page.updated_at = $3 AND page.draft IS NOT NULL
	RETURNING page.id
), reordered AS (
	UPDATE event_pages AS page
	SET navigation_order = desired.position, updated_at = $12
	FROM desired
	WHERE page.event_id = $2 AND page.id = desired.id AND page.id <> $1
	  AND page.navigation_order <> desired.position
	  AND EXISTS (SELECT 1 FROM published)
	RETURNING page.id
)
SELECT (SELECT count(*) FROM published) + 0 * (SELECT count(*) FROM reordered)`
	var published int64
	err := q.db.QueryRow(ctx, query, arg.ID, arg.EventID, arg.ExpectedUpdatedAt, arg.Slug, arg.Title, arg.Document,
		arg.Visibility, arg.Navigation, arg.PageIDs, arg.ChallengesPosition, arg.ResultsPosition, arg.Now).Scan(&published)
	return published, err
}
