package eventAnalytics

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
)

const (
	// labRecomputeHold is how long the jobs stay off the event while it is
	// recomputed; every loop renews it. After a failed run it stays that long, so
	// the half-restored sources are not rolled up by the regular job.
	labRecomputeHold = 24 * time.Hour
	// labRecomputeChunksPerPage archive chunks are read and restored at a time.
	labRecomputeChunksPerPage = 3
)

// LabTelemetryRecompute is what a recompute did.
type LabTelemetryRecompute struct {
	// ArchiveChunks were read from the archive and RestoredRows of them put
	// back into the raw table (rows already there are not counted).
	ArchiveChunks int
	RestoredRows  int64
	// Observations are the raw rows the rollups read again.
	UsageObservations   int
	JournalObservations int
}

// ResolveEvent turns an event id or tag into the event's id; a tag shared by
// several events (an archived one) is refused.
func (u *EventAnalyticsUseCase) ResolveEvent(ctx context.Context, idOrTag string) (uuid.UUID, error) {
	if id, err := uuid.FromString(idOrTag); err == nil {
		return id, nil
	}
	ids, err := u.store.EventIDsByTag(ctx, idOrTag)
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to look up the event by tag").Err()
	}
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return uuid.Nil, model.ErrPlatform.WithMessage("No event with this tag").Err()
	default:
		return uuid.Nil, model.ErrPlatform.WithMessage("Several events carry this tag, give the event id").Err()
	}
}

// RecomputeLabTelemetry rebuilds the lab usage minute tables (devices, labs,
// VPN clients) and the transitions journal of one event from the raw
// observations: the archived ones are restored into the raw table first, the
// ones still there are used as they are. The analytics and retention jobs
// leave the event alone meanwhile. Run it once for an event whose rollups were
// built before they existed, or after fixing one. The restored rows are
// deleted again by the retention job after the usual day, since the archive
// already holds them.
//
// The rebuild starts by deleting what it rebuilds, so it refuses an event that
// has data older than the archive and the raw rows reach (the participants
// period has passed for the sources, not yet for the rollups) unless force is
// set: that data would be lost. The hold stays on after a failure (it expires on
// its own after a day), so the half-restored sources are never rolled up by the
// regular job; a repeated run starts over.
func (u *EventAnalyticsUseCase) RecomputeLabTelemetry(ctx context.Context, eventID uuid.UUID, force bool) (LabTelemetryRecompute, error) {
	var res LabTelemetryRecompute
	fail := func(err error, msg string) (LabTelemetryRecompute, error) {
		return res, model.ErrPlatform.WithError(err).WithMessage(msg).Err()
	}
	dataFrom, coveredFrom, err := u.store.LabTelemetryCoverage(ctx, eventID)
	if err != nil {
		return fail(err, "Failed to check what the lab telemetry can be rebuilt from")
	}
	if dataFrom != nil && (coveredFrom == nil || dataFrom.Before(coveredFrom.Add(-2*time.Minute))) && !force {
		return res, model.ErrPlatform.WithMessage(fmt.Sprintf(
			"The event has lab telemetry from %s, but the archive and raw rows it would be rebuilt from start only at %s: "+
				"recomputing would erase what cannot be rebuilt. Pass --force to do it anyway",
			dataFrom.Format(time.RFC3339), coveredFromText(coveredFrom))).Err()
	}

	// renew keeps the hold for the whole run: every loop below calls it.
	renew := func() error {
		until := u.now().Add(labRecomputeHold)
		return u.store.HoldLabTelemetry(context.WithoutCancel(ctx), eventID, &until)
	}
	if err = renew(); err != nil {
		return fail(err, "Failed to hold the event's lab telemetry")
	}
	// Statements that started before the hold still finish (the rollup statements check the hold themselves).
	select {
	case <-time.After(u.settle):
	case <-ctx.Done():
		return fail(ctx.Err(), "The recompute was interrupted")
	}

	if err = u.store.ResetLabTelemetry(ctx, eventID); err != nil {
		return fail(err, "Failed to reset the event's lab telemetry rollups")
	}
	var after eventAnalyticsRepo.VPNCursor
	for {
		chunks, cerr := u.store.LabArchiveChunks(ctx, eventID, after, labRecomputeChunksPerPage)
		if cerr != nil {
			return fail(cerr, "Failed to read the lab observation archive")
		}
		if len(chunks) == 0 {
			break
		}
		if err = renew(); err != nil {
			return fail(err, "Failed to renew the hold")
		}
		for _, chunk := range chunks {
			restored, rerr := u.store.RestoreLabArchive(ctx, chunk)
			if rerr != nil {
				return fail(rerr, "Failed to restore an archive chunk")
			}
			res.ArchiveChunks++
			res.RestoredRows += restored
			after = chunk.Last
		}
	}

	var cursor eventAnalyticsRepo.VPNCursor
	for {
		if err = renew(); err != nil {
			return fail(err, "Failed to renew the hold")
		}
		next, read, uerr := u.store.RollupLabUsage(ctx, eventID, cursor, labUsageBatchSize, true)
		if uerr != nil {
			return fail(uerr, "Failed to roll up the lab usage")
		}
		res.UsageObservations += read
		cursor = next
		if read < labUsageBatchSize {
			break
		}
	}
	journal, err := u.store.LabJournal(ctx, eventID, u.newID)
	if err != nil {
		return fail(err, "Failed to load the transitions journal")
	}
	cursor = eventAnalyticsRepo.VPNCursor{}
	for {
		if err = renew(); err != nil {
			return fail(err, "Failed to renew the hold")
		}
		next, read, jerr := u.store.RollupLabJournal(ctx, eventID, cursor, labJournalBatchSize, journal, true)
		if jerr != nil {
			return fail(jerr, "Failed to build the transitions journal")
		}
		res.JournalObservations += read
		cursor = next
		if read < labJournalBatchSize {
			break
		}
		if ctx.Err() != nil {
			return fail(ctx.Err(), "The recompute was interrupted")
		}
	}
	if err = u.store.HoldLabTelemetry(context.WithoutCancel(ctx), eventID, nil); err != nil {
		log.Error().Err(err).Str("event_id", eventID.String()).Msg("lab telemetry recompute: release the hold")
	}
	log.Info().Str("event_id", eventID.String()).Int("archive_chunks", res.ArchiveChunks).
		Int64("restored_rows", res.RestoredRows).Int("usage_observations", res.UsageObservations).
		Int("journal_observations", res.JournalObservations).Msg("lab telemetry recompute: done")
	return res, nil
}

func coveredFromText(t *time.Time) string {
	if t == nil {
		return "never (nothing is left)"
	}
	return t.Format(time.RFC3339)
}
