package eventTeamRepo

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// fieldPolicyQueries back «required for everyone» on team fields.
type fieldPolicyQueries interface {
	ListEventTeamExtraFields(ctx context.Context, eventID uuid.UUID) ([]postgres.ListEventTeamExtraFieldsRow, error)
	SetEventTeamsFieldsMissing(ctx context.Context, arg postgres.SetEventTeamsFieldsMissingParams) error
	GetEventTeamFieldsMissing(ctx context.Context, arg postgres.GetEventTeamFieldsMissingParams) (int32, error)
}

// TeamFields is one team's saved field answers.
type TeamFields struct {
	TeamID uuid.UUID
	Values map[string]any
}

// ListTeamFields returns the saved field answers of every team of the event.
func (r *Repository) ListTeamFields(ctx context.Context, eventID uuid.UUID) ([]TeamFields, error) {
	rows, err := r.q.ListEventTeamExtraFields(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]TeamFields, 0, len(rows))
	for _, row := range rows {
		var values map[string]any
		if len(row.ExtraFields) > 0 {
			if err = json.Unmarshal(row.ExtraFields, &values); err != nil {
				return nil, err
			}
		}
		out = append(out, TeamFields{TeamID: row.ID, Values: values})
	}
	return out, nil
}

// SetFieldsMissing stores the recounted missing-field numbers by team.
func (r *Repository) SetFieldsMissing(ctx context.Context, eventID uuid.UUID, missing map[uuid.UUID]int32) error {
	if len(missing) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, 0, len(missing))
	counts := make([]int32, 0, len(missing))
	for id, count := range missing {
		ids = append(ids, id)
		counts = append(counts, count)
	}
	return r.q.SetEventTeamsFieldsMissing(ctx, postgres.SetEventTeamsFieldsMissingParams{EventID: eventID, TeamIds: ids, Missing: counts})
}

// FieldsMissing is how many required team fields the team has not filled.
func (r *Repository) FieldsMissing(ctx context.Context, eventID, teamID uuid.UUID) (int32, error) {
	return r.q.GetEventTeamFieldsMissing(ctx, postgres.GetEventTeamFieldsMissingParams{EventID: eventID, TeamID: teamID})
}
