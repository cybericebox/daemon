package eventAnalyticsRepo

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// JournalQueries are the statements of the lab transitions journal.
type JournalQueries interface {
	ListEventLabJournalStates(context.Context, uuid.UUID) ([]postgres.ListEventLabJournalStatesRow, error)
	SaveEventLabJournalBatch(context.Context, postgres.SaveEventLabJournalBatchParams) error
}

// LabJournal loads the stored entity states of the event into a journal.
func (r *Repository) LabJournal(
	ctx context.Context, eventID uuid.UUID, newID func() uuid.UUID,
) (*eventAnalyticsModel.LabJournal, error) {
	rows, err := r.q.ListEventLabJournalStates(ctx, eventID)
	if err != nil {
		return nil, err
	}
	stored := make([]eventAnalyticsModel.LabStateRow, 0, len(rows))
	for _, row := range rows {
		var state eventAnalyticsModel.LabEntityState
		if err = json.Unmarshal(row.State, &state); err != nil {
			return nil, err
		}
		stored = append(stored, eventAnalyticsModel.LabStateRow{
			LabEntityKey: eventAnalyticsModel.LabEntityKey{
				TeamID: row.TeamID, LabGroupName: row.LabGroupName, Kind: row.Kind, LabName: row.LabName, Name: row.Name,
			},
			State: state,
		})
	}
	return eventAnalyticsModel.NewLabJournal(stored, newID), nil
}

// RollupLabJournal feeds up to batchSize observations after the cursor, in
// arrival order, to the journal and stores the transitions it finds, the
// changed states and the moved cursor in one statement. It returns the cursor
// after the last observation and how many it read (fewer than batchSize:
// caught up). After an error the journal must be reloaded.
func (r *Repository) RollupLabJournal(
	ctx context.Context, eventID uuid.UUID, after VPNCursor, batchSize int32, journal *eventAnalyticsModel.LabJournal,
) (VPNCursor, int, error) {
	observations, err := r.RawLabObservations(ctx, eventID, after, batchSize)
	if err != nil {
		return after, 0, err
	}
	if len(observations) == 0 {
		return after, 0, nil
	}
	transitions := make([]eventAnalyticsModel.LabTransition, 0)
	for _, o := range observations {
		transitions = append(transitions, journal.Apply(o)...)
	}
	changed := journal.Changed()
	transitionsJSON, err := json.Marshal(transitions)
	if err != nil {
		return after, 0, err
	}
	statesJSON, err := json.Marshal(changed)
	if err != nil {
		return after, 0, err
	}
	last := observations[len(observations)-1]
	err = r.q.SaveEventLabJournalBatch(ctx, postgres.SaveEventLabJournalBatchParams{
		EventID:          eventID,
		CursorReceivedAt: pgtype.Timestamptz{Time: last.ReceivedAt, Valid: true},
		CursorID:         uuid.NullUUID{UUID: last.ID, Valid: true},
		Transitions:      transitionsJSON, States: statesJSON,
	})
	if err != nil {
		return after, 0, err
	}
	return VPNCursor{ReceivedAt: last.ReceivedAt, ID: last.ID}, len(observations), nil
}
