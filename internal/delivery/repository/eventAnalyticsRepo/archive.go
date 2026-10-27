package eventAnalyticsRepo

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// ArchiveQueries are the statements of the raw lab observation archive.
type ArchiveQueries interface {
	NextEventLabObservationArchiveRange(
		context.Context, postgres.NextEventLabObservationArchiveRangeParams,
	) ([]postgres.NextEventLabObservationArchiveRangeRow, error)
	ListEventLabObservationArchiveLines(context.Context, postgres.ListEventLabObservationArchiveLinesParams) ([]postgres.ListEventLabObservationArchiveLinesRow, error)
	SaveEventLabObservationArchive(context.Context, postgres.SaveEventLabObservationArchiveParams) error
	ListEventLabObservationArchiveChunks(
		context.Context, postgres.ListEventLabObservationArchiveChunksParams,
	) ([]postgres.ListEventLabObservationArchiveChunksRow, error)
	ListEventLabObservationsAfter(
		context.Context, postgres.ListEventLabObservationsAfterParams,
	) ([]postgres.ListEventLabObservationsAfterRow, error)
	SetEventLabTelemetryHold(context.Context, postgres.SetEventLabTelemetryHoldParams) error
	ResetEventLabTelemetry(context.Context, uuid.UUID) error
	GetEventLabTelemetryCoverage(context.Context, uuid.UUID) (postgres.GetEventLabTelemetryCoverageRow, error)
	RestoreEventLabObservations(context.Context, []byte) (int64, error)
	ListEventIDsByTag(context.Context, string) ([]uuid.UUID, error)
}

// LabArchiveChunk is one stored chunk of the raw observation archive.
type LabArchiveChunk struct {
	ID          uuid.UUID
	PeriodStart time.Time
	PeriodEnd   time.Time
	RowCount    int32
	// Last is the arrival position of the chunk's last row.
	Last  VPNCursor
	Codec string
	Data  []byte
}

// ArchiveLabObservations compresses the next chunk of the event's raw
// observations after the archive cursor into the archive and moves the
// cursor past it, atomically. Only observations received before closedBefore
// are taken. It returns the cursor after the chunk and the rows archived
// (0: nothing left to archive).
func (r *Repository) ArchiveLabObservations(
	ctx context.Context, eventID uuid.UUID, after VPNCursor, closedBefore time.Time, chunkID uuid.UUID,
) (VPNCursor, int, error) {
	if after.ReceivedAt.IsZero() {
		after.ReceivedAt = cursorStart
	}
	ranges, err := r.q.NextEventLabObservationArchiveRange(ctx, postgres.NextEventLabObservationArchiveRangeParams{
		EventID: eventID, ClosedBefore: closedBefore, AfterReceivedAt: after.ReceivedAt, AfterID: after.ID,
		MaxRows: eventAnalyticsModel.LabArchiveChunkRows,
	})
	if err != nil {
		return after, 0, err
	}
	if len(ranges) == 0 || ranges[0].RowCount == 0 {
		return after, 0, nil
	}
	chunk := ranges[0]
	rows, err := r.q.ListEventLabObservationArchiveLines(ctx, postgres.ListEventLabObservationArchiveLinesParams{
		EventID: eventID, AfterReceivedAt: after.ReceivedAt, AfterID: after.ID,
		LastReceivedAt: chunk.LastReceivedAt, LastID: chunk.LastID,
	})
	if err != nil {
		return after, 0, err
	}
	if len(rows) == 0 {
		return after, 0, errors.New("lab archive: the chunk range holds no rows")
	}
	lines := make([]string, 0, len(rows))
	rowIDs := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		lines = append(lines, row.Line)
		rowIDs = append(rowIDs, row.ID)
	}
	data, err := eventAnalyticsModel.CompressLabArchive(lines)
	if err != nil {
		return after, 0, err
	}
	err = r.q.SaveEventLabObservationArchive(ctx, postgres.SaveEventLabObservationArchiveParams{
		ID: chunkID, EventID: eventID,
		PeriodStart: chunk.PeriodStart, PeriodEnd: chunk.PeriodStart.Add(time.Hour), RowCount: int32(len(lines)), //nolint:gosec // bounded by LabArchiveChunkRows
		RowIds:          rowIDs,
		FirstReceivedAt: chunk.FirstReceivedAt, FirstID: chunk.FirstID,
		LastReceivedAt: pgtype.Timestamptz{Time: chunk.LastReceivedAt, Valid: true},
		LastID:         uuid.NullUUID{UUID: chunk.LastID, Valid: true},
		Codec:          eventAnalyticsModel.LabArchiveCodecZstdNDJSON, Data: data,
	})
	if err != nil {
		return after, 0, err
	}
	return VPNCursor{ReceivedAt: chunk.LastReceivedAt, ID: chunk.LastID}, len(lines), nil
}

// LabArchiveChunks pages through the event's stored chunks in arrival order,
// after the given position.
func (r *Repository) LabArchiveChunks(
	ctx context.Context, eventID uuid.UUID, after VPNCursor, limit int32,
) ([]LabArchiveChunk, error) {
	if after.ReceivedAt.IsZero() {
		after.ReceivedAt = cursorStart
	}
	rows, err := r.q.ListEventLabObservationArchiveChunks(ctx, postgres.ListEventLabObservationArchiveChunksParams{
		EventID: eventID, AfterReceivedAt: after.ReceivedAt, AfterID: after.ID, LimitRows: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]LabArchiveChunk, 0, len(rows))
	for _, row := range rows {
		out = append(out, LabArchiveChunk{
			ID: row.ID, PeriodStart: row.PeriodStart, PeriodEnd: row.PeriodEnd, RowCount: row.RowCount,
			Last: VPNCursor{ReceivedAt: row.LastReceivedAt, ID: row.LastID}, Codec: row.Codec, Data: row.Data,
		})
	}
	return out, nil
}

// RawLabObservations reads up to limit observations still in the raw table
// after the given position, in arrival order.
func (r *Repository) RawLabObservations(
	ctx context.Context, eventID uuid.UUID, after VPNCursor, limit int32,
) ([]eventAnalyticsModel.LabObservation, error) {
	if after.ReceivedAt.IsZero() {
		after.ReceivedAt = cursorStart
	}
	rows, err := r.q.ListEventLabObservationsAfter(ctx, postgres.ListEventLabObservationsAfterParams{
		EventID: eventID, AfterReceivedAt: after.ReceivedAt, AfterID: after.ID, LimitRows: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]eventAnalyticsModel.LabObservation, 0, len(rows))
	for _, row := range rows {
		o, derr := eventAnalyticsModel.ParseLabObservationLine(row.Line)
		if derr != nil {
			return nil, derr
		}
		out = append(out, o)
	}
	return out, nil
}

// HoldLabTelemetry keeps the analytics and retention jobs off the event's lab
// telemetry until the given time; nil releases the hold.
func (r *Repository) HoldLabTelemetry(ctx context.Context, eventID uuid.UUID, until *time.Time) error {
	return r.q.SetEventLabTelemetryHold(ctx, postgres.SetEventLabTelemetryHoldParams{
		EventID: eventID, HoldUntil: timestamptz(until),
	})
}

// ResetLabTelemetry deletes the minute tables, the transitions journal and its
// states of the event and rewinds the lab usage and journal cursors.
func (r *Repository) ResetLabTelemetry(ctx context.Context, eventID uuid.UUID) error {
	return r.q.ResetEventLabTelemetry(ctx, eventID)
}

// RestoreLabArchive puts the rows of an archive chunk back into the raw
// observation table (rows already there are left alone) and returns how many
// it inserted.
func (r *Repository) RestoreLabArchive(ctx context.Context, chunk LabArchiveChunk) (int64, error) {
	var rows bytes.Buffer
	rows.WriteByte('[')
	first := true
	err := eventAnalyticsModel.LabArchiveLines(chunk.Codec, chunk.Data, func(line []byte) error {
		if !first {
			rows.WriteByte(',')
		}
		first = false
		rows.Write(line)
		return nil
	})
	if err != nil {
		return 0, err
	}
	rows.WriteByte(']')
	return r.q.RestoreEventLabObservations(ctx, rows.Bytes())
}

// EventIDsByTag lists the events carrying the tag, oldest first.
func (r *Repository) EventIDsByTag(ctx context.Context, tag string) ([]uuid.UUID, error) {
	return r.q.ListEventIDsByTag(ctx, tag)
}

// LabTelemetryCoverage tells where the event's derived lab telemetry starts and where the archive and the raw rows
// it can be rebuilt from start; nil: there is none.
func (r *Repository) LabTelemetryCoverage(ctx context.Context, eventID uuid.UUID) (dataFrom, coveredFrom *time.Time, err error) {
	row, err := r.q.GetEventLabTelemetryCoverage(ctx, eventID)
	if err != nil {
		return nil, nil, err
	}
	unix := func(v int64) *time.Time {
		if v < 0 {
			return nil
		}
		t := time.Unix(v, 0).UTC()
		return &t
	}
	return unix(row.DataFrom), unix(row.CoveredFrom), nil
}
