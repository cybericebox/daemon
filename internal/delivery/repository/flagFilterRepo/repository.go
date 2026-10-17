// Package flagFilterRepo stores the issued-flag filters of event task variants
// (one row per event exercise, variant and subtask, rewritten in place) and the
// reads a rebuild needs: the stored expected flags and the pinned candidates.
// Every read and write runs under the event exercise's variant pick lock.
package flagFilterRepo

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/flagfilter"
)

type Queries interface {
	GetEventFlagFilter(context.Context, postgres.GetEventFlagFilterParams) (postgres.EventFlagFilter, error)
	UpsertEventFlagFilter(context.Context, postgres.UpsertEventFlagFilterParams) error
	ListEventFlagFilters(context.Context, uuid.UUID) ([]postgres.ListEventFlagFiltersRow, error)
	ListTaskVariantIssuedFlags(context.Context, postgres.ListTaskVariantIssuedFlagsParams) ([]string, error)
	ListEventIssuedFlags(context.Context, uuid.UUID) ([]postgres.ListEventIssuedFlagsRow, error)
	ListVersionTaskFlags(context.Context, []uuid.UUID) ([]postgres.ListVersionTaskFlagsRow, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// Key is one task variant of an event exercise.
type Key struct {
	EventExerciseID uuid.UUID
	VariantIndex    int32
	TaskID          uuid.UUID
}

// Get is the stored filter of a key (a not-found error when there is none).
func (r *Repository) Get(ctx context.Context, key Key) (*flagfilter.Filter, error) {
	row, err := r.q.GetEventFlagFilter(ctx, postgres.GetEventFlagFilterParams{EventExerciseID: key.EventExerciseID, VariantIndex: key.VariantIndex, TaskID: key.TaskID})
	if err != nil {
		return nil, err
	}
	return &flagfilter.Filter{Fingerprint: row.Fingerprint, Mode: flagfilter.Mode(row.Mode), Salt: row.Salt, Hashes: int(row.Hashes), Size: uint64(max(row.Size, 0)), Bits: row.Bits,
		Inserted: int(row.Inserted), Capacity: int(row.Capacity), Resets: int(row.Resets)}, nil
}

// Save writes the whole filter of a key (insert once, then in place).
func (r *Repository) Save(ctx context.Context, key Key, f *flagfilter.Filter) error {
	return r.q.UpsertEventFlagFilter(ctx, postgres.UpsertEventFlagFilterParams{EventExerciseID: key.EventExerciseID, VariantIndex: key.VariantIndex, TaskID: key.TaskID,
		Fingerprint: f.Fingerprint, Mode: int16(f.Mode), Salt: f.Salt, Hashes: int16(f.Hashes), Size: int64(f.Size), Bits: f.Bits, Inserted: int32(f.Inserted), Capacity: int32(f.Capacity), Resets: int32(f.Resets)})
}

// Resets are the reset counts of the event's filters by key.
func (r *Repository) Resets(ctx context.Context, eventID uuid.UUID) (map[Key]int, error) {
	rows, err := r.q.ListEventFlagFilters(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make(map[Key]int, len(rows))
	for _, row := range rows {
		out[Key{row.EventExerciseID, row.VariantIndex, row.TaskID}] = int(row.Resets)
	}
	return out, nil
}

// Issued are the stored expected flags of one key.
func (r *Repository) Issued(ctx context.Context, key Key) ([]string, error) {
	return r.q.ListTaskVariantIssuedFlags(ctx, postgres.ListTaskVariantIssuedFlagsParams{EventExerciseID: key.EventExerciseID, TaskID: key.TaskID, VariantIndex: key.VariantIndex})
}

// EventIssued are the stored expected flags of an event by key, in one pass.
func (r *Repository) EventIssued(ctx context.Context, eventID uuid.UUID) (map[Key][]string, error) {
	rows, err := r.q.ListEventIssuedFlags(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := map[Key][]string{}
	for _, row := range rows {
		key := Key{row.EventExerciseID, row.VariantIndex, row.TaskID}
		out[key] = append(out[key], row.ExpectedFlag)
	}
	return out, nil
}

// TaskFlags are the flag candidates of pinned versions: version id, then
// variant position, then task id.
type TaskFlags map[uuid.UUID][]map[uuid.UUID][]string

// VersionTaskFlags reads the candidates of the tasks of pinned versions.
func (r *Repository) VersionTaskFlags(ctx context.Context, versionIDs []uuid.UUID) (TaskFlags, error) {
	out := TaskFlags{}
	if len(versionIDs) == 0 {
		return out, nil
	}
	rows, err := r.q.ListVersionTaskFlags(ctx, versionIDs)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		var variants []struct {
			Tasks []struct {
				ID   uuid.UUID `json:"id"`
				Flag []string  `json:"flag"`
			} `json:"tasks"`
		}
		if err = json.Unmarshal(row.Variants, &variants); err != nil {
			return nil, err
		}
		list := make([]map[uuid.UUID][]string, len(variants))
		for i, variant := range variants {
			list[i] = make(map[uuid.UUID][]string, len(variant.Tasks))
			for _, task := range variant.Tasks {
				list[i][task.ID] = task.Flag
			}
		}
		out[row.VersionID] = list
	}
	return out, nil
}
