// Package platformStandRepo is the platform-wide, read-only view of team
// stands across events (query-side shapes, like the other list/stat reads).
// A stand itself is still written only by the event stand engine.
package platformStandRepo

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

type Queries interface {
	ListPlatformStands(context.Context, postgres.ListPlatformStandsParams) ([]postgres.ListPlatformStandsRow, error)
	ListPlatformStandEvents(context.Context) ([]postgres.ListPlatformStandEventsRow, error)
	CountPlatformStandsByStatus(context.Context) ([]postgres.CountPlatformStandsByStatusRow, error)
	CountPlatformStandsByKindStatus(context.Context) ([]postgres.CountPlatformStandsByKindStatusRow, error)
	ListPlatformTestLabs(context.Context, postgres.ListPlatformTestLabsParams) ([]postgres.ListPlatformTestLabsRow, error)
	GetPlatformTestLab(context.Context, uuid.UUID) (postgres.GetPlatformTestLabRow, error)
	CountPlatformTestLabs(context.Context, time.Time) (postgres.CountPlatformTestLabsRow, error)
	FacetPlatformStands(context.Context, postgres.FacetPlatformStandsParams) ([]postgres.FacetPlatformStandsRow, error)
	FacetPlatformTestLabs(context.Context, postgres.FacetPlatformTestLabsParams) ([]postgres.FacetPlatformTestLabsRow, error)
}

// Stand kinds of the list filter.
const (
	KindEvent      = "event"
	KindModerators = "moderators"
)

type (
	Repository struct{ q Queries }

	// TestLab is one catalog test lab (an exercise test deploy) with its author and exercise.
	TestLab struct {
		ID            uuid.UUID
		GroupName     string
		LabName       string
		ExerciseID    uuid.UUID
		ExerciseName  string
		VariantNumber int32
		AuthorID      uuid.UUID
		AuthorName    string
		AuthorEmail   string
		CreatedAt     time.Time
		ExpiresAt     time.Time
	}

	// TestLabRef is the handle of one test lab, enough to tear it down.
	TestLabRef struct {
		ID        uuid.UUID
		GroupName string
		LabName   string
		OwnerID   uuid.UUID
		// Devices are the logical devices (id, name, type only) of the topology the lab was deployed from.
		Devices []exerciseModel.Device
	}

	// TestLabCounts split the test labs by lease.
	TestLabCounts struct{ Active, Expired int64 }

	// StandKindCounts is the stand count per persisted status of one kind.
	StandKindCounts map[eventStandModel.Status]int64

	// Stand is one team stand with the event and team it belongs to.
	Stand struct {
		EventID         uuid.UUID
		EventName       string
		EventTag        string
		TeamID          uuid.UUID
		TeamName        string
		Moderators      bool
		Status          eventStandModel.Status
		Reason          string
		UpdatedAt       time.Time
		StatusChangedAt time.Time
		Generation      int32
	}

	Filter struct {
		EventID  uuid.NullUUID
		Statuses []eventStandModel.Status
		// Kind is "" for every stand, KindEvent or KindModerators.
		Kind   string
		Search string
		// SortBy is event, team, status, updatedAt, statusChangedAt or generation ("": the newest change first).
		SortBy, SortDir string
		Limit           int32
		Offset          int32
	}

	EventRef struct {
		ID   uuid.UUID
		Name string
		Tag  string
	}
)

func New(q Queries) *Repository { return &Repository{q: q} }

// List returns one page of stands and the total row count of the filter.
func (r *Repository) List(ctx context.Context, filter Filter) ([]Stand, int64, error) {
	var statuses []int16
	if len(filter.Statuses) > 0 {
		statuses = make([]int16, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			statuses = append(statuses, int16(status))
		}
	}
	rows, err := r.q.ListPlatformStands(ctx, postgres.ListPlatformStandsParams{
		EventID: filter.EventID, Statuses: statuses, Kind: filter.Kind, Search: filter.Search, LimitVal: filter.Limit, OffsetVal: filter.Offset,
		SortBy: filter.SortBy, SortDir: filter.SortDir,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]Stand, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.Total
		out = append(out, Stand{
			EventID: row.EventID, EventName: row.EventName, EventTag: row.EventTag, TeamID: row.EventTeamID, TeamName: row.TeamName,
			Moderators: row.Moderators, Status: eventStandModel.Status(row.Status), Reason: row.Reason,
			UpdatedAt: row.UpdatedAt, StatusChangedAt: row.StatusChangedAt, Generation: row.Generation,
		})
	}
	return out, total, nil
}

// Events lists the events that have at least one stand.
func (r *Repository) Events(ctx context.Context) ([]EventRef, error) {
	rows, err := r.q.ListPlatformStandEvents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]EventRef, 0, len(rows))
	for _, row := range rows {
		out = append(out, EventRef{ID: row.ID, Name: row.Name, Tag: row.Tag})
	}
	return out, nil
}

// CountByStatus returns the stand count per persisted status.
func (r *Repository) CountByStatus(ctx context.Context) (map[eventStandModel.Status]int64, error) {
	rows, err := r.q.CountPlatformStandsByStatus(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[eventStandModel.Status]int64, len(rows))
	for _, row := range rows {
		out[eventStandModel.Status(row.Status)] = row.Total
	}
	return out, nil
}

// CountByKind returns the stand count per status for event teams and for the moderators team.
func (r *Repository) CountByKind(ctx context.Context) (event, moderators StandKindCounts, err error) {
	rows, err := r.q.CountPlatformStandsByKindStatus(ctx)
	if err != nil {
		return nil, nil, err
	}
	event, moderators = StandKindCounts{}, StandKindCounts{}
	for _, row := range rows {
		target := event
		if row.Moderators {
			target = moderators
		}
		target[eventStandModel.Status(row.Status)] += row.Total
	}
	return event, moderators, nil
}

// ListTestLabs returns one page of the catalog test labs, newest first, and the total.
func (r *Repository) ListTestLabs(ctx context.Context, search string, limit, offset int32) ([]TestLab, int64, error) {
	return r.ListTestLabsFiltered(ctx, TestLabFilter{Search: search, Limit: limit, Offset: offset})
}

// TestLabFilter narrows and orders the test labs. Status is "", "active" (the lease runs) or "expired"; SortBy is
// exercise, author, createdAt or expiresAt ("": the newest first).
type TestLabFilter struct {
	Search               string
	Status               string
	AuthorID, ExerciseID uuid.NullUUID
	SortBy, SortDir      string
	Now                  time.Time
	Limit, Offset        int32
}

// ListTestLabsFiltered returns one page of the catalog test labs and the total behind the filter.
func (r *Repository) ListTestLabsFiltered(
	ctx context.Context,
	f TestLabFilter,
) ([]TestLab, int64, error) {
	rows, err := r.q.ListPlatformTestLabs(ctx, postgres.ListPlatformTestLabsParams{
		Search:     f.Search,
		Status:     f.Status,
		Now:        f.Now,
		AuthorID:   f.AuthorID,
		ExerciseID: f.ExerciseID,
		SortBy:     f.SortBy,
		SortDir:    f.SortDir,
		LimitVal:   f.Limit,
		OffsetVal:  f.Offset,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]TestLab, 0, len(rows))
	var total int64
	for _, row := range rows {
		total = row.Total
		out = append(out, TestLab{
			ID: row.ID, GroupName: row.GroupName, LabName: row.LabName, ExerciseID: row.ExerciseID, ExerciseName: row.ExerciseName, VariantNumber: row.VariantNumber,
			AuthorID: row.CreatedBy, AuthorName: strings.TrimSpace(row.AuthorFirstName + " " + row.AuthorLastName), AuthorEmail: row.AuthorEmail,
			CreatedAt: row.CreatedAt, ExpiresAt: row.ExpiresAt,
		})
	}
	return out, total, nil
}

// GetTestLab loads the handle of one test lab; the error is the repository's not-found when it is gone.
func (r *Repository) GetTestLab(ctx context.Context, id uuid.UUID) (TestLabRef, error) {
	row, err := r.q.GetPlatformTestLab(ctx, id)
	if err != nil {
		return TestLabRef{}, err
	}
	ref := TestLabRef{ID: row.ID, GroupName: row.GroupName, LabName: row.LabName, OwnerID: row.CreatedBy}
	// Names and types are a hint for the screen: a topology that cannot be read leaves them out.
	if json.Unmarshal(row.Devices, &ref.Devices) != nil {
		ref.Devices = nil
	}
	return ref, nil
}

// CountTestLabs counts the test labs whose lease is running and those still waiting for cleanup.
func (r *Repository) CountTestLabs(ctx context.Context, now time.Time) (TestLabCounts, error) {
	row, err := r.q.CountPlatformTestLabs(ctx, now)
	if err != nil {
		return TestLabCounts{}, err
	}
	return TestLabCounts{Active: row.Active, Expired: row.Expired}, nil
}

// StandFacetFields are the fields of the stand table that have a facet.
func StandFacetFields() []string { return []string{"status", "kind", "event"} }

// StandFacet counts the stands per value of one field (status, kind or event) under the other filters of the table;
// the field's own filter is ignored. Status keys are the status names; values with no stand are absent.
func (r *Repository) StandFacet(ctx context.Context, field string, filter Filter) (map[string]int64, error) {
	var statuses []int16
	for _, status := range filter.Statuses {
		statuses = append(statuses, int16(status))
	}
	rows, err := r.q.FacetPlatformStands(ctx, postgres.FacetPlatformStandsParams{
		Field: field, EventID: filter.EventID, Statuses: statuses, Kind: filter.Kind, Search: filter.Search,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows)+1)
	for _, row := range rows {
		key := row.Key
		if field == "status" {
			n, convErr := strconv.Atoi(row.Key)
			if convErr != nil {
				continue
			}
			key = eventStandModel.Status(n).String()
		}
		out[key] += row.Count
	}
	if field == "status" {
		if active := out[eventStandModel.StatusCreating.String()] + out[eventStandModel.StatusReady.String()]; active > 0 {
			out["active"] = active
		}
	}
	return out, nil
}

// TestLabFacetFields are the fields of the test lab table that have a facet.
func TestLabFacetFields() []string { return []string{"status", "author", "exercise"} }

// TestLabFacet counts the test labs per value of one field (status, author or exercise) under the other filters of
// the table; the field's own filter is ignored.
func (r *Repository) TestLabFacet(ctx context.Context, field string, f TestLabFilter) (map[string]int64, error) {
	rows, err := r.q.FacetPlatformTestLabs(ctx, postgres.FacetPlatformTestLabsParams{
		Field: field, Now: f.Now, Search: f.Search, Status: f.Status, AuthorID: f.AuthorID, ExerciseID: f.ExerciseID,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		out[row.Key] += row.Count
	}
	return out, nil
}
