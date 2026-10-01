package eventAnalytics

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

const (
	// rollupEventsPerPass bounds one pass; a backfill of past events spreads
	// over several passes, running events always come first.
	rollupEventsPerPass = 50
	// vpnBatchSize observations are read per statement, at most
	// vpnBatchesPerEvent times per event and pass.
	vpnBatchSize       = 2000
	vpnBatchesPerEvent = 10
)

// RefreshEventAnalytics is one pass of the analytics rollup job (§5): for
// every started event with work left it rebuilds the 5-minute buckets and
// folds new telemetry into VPN sessions (D5). An event is finalized after its
// finish (plus a grace period) and then left alone, unless its results
// change again. Every step is idempotent; a failing event does not stop the
// others, and past events are backfilled the same way.
func (u *EventAnalyticsUseCase) RefreshEventAnalytics(ctx context.Context) error {
	started := u.now()
	candidates, err := u.store.RollupCandidates(ctx, started, rollupEventsPerPass)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list events for analytics").Err()
	}
	var errs []error
	var buckets, vpn int
	for _, c := range candidates {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if !c.BucketsFinalized || c.BucketsRevision != c.Revision {
			if err = u.refreshBuckets(ctx, c, started); err != nil {
				errs = append(errs, err)
			} else {
				buckets++
			}
		}
		if c.InfrastructureAllowed && !c.VPNFinalized {
			if err = u.refreshVPNSessions(ctx, c, started); err != nil {
				errs = append(errs, err)
			} else {
				vpn++
			}
		}
	}
	err = errors.Join(errs...)
	log.Debug().Err(err).Int("events", len(candidates)).Int("buckets", buckets).Int("vpn", vpn).
		Dur("took", u.now().Sub(started)).Msg("event analytics: rollup pass")
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to refresh event analytics").Err()
	}
	return nil
}

func (u *EventAnalyticsUseCase) refreshBuckets(ctx context.Context, c eventAnalyticsRepo.RollupCandidate, now time.Time) error {
	if err := u.store.RefreshBuckets(ctx, c.EventID); err != nil {
		return err
	}
	var finalized *time.Time
	if eventAnalyticsModel.Final(c.FinishedAt, now) {
		finalized = &now
	}
	return u.store.MarkBucketsRefreshed(ctx, c.EventID, now, c.Revision, finalized)
}

// refreshVPNSessions reads the observations after the event's cursor batch
// by batch and folds their handshakes into sessions. The sessions are saved
// before the cursor moves, so a failure re-reads (harmlessly) rather than
// loses data. Once caught up after the event is final, the rollup closes.
func (u *EventAnalyticsUseCase) refreshVPNSessions(ctx context.Context, c eventAnalyticsRepo.RollupCandidate, now time.Time) error {
	cursor := c.VPNCursor
	caughtUp := false
	for i := 0; i < vpnBatchesPerEvent && !caughtUp; i++ {
		samples, next, read, err := u.store.VPNSamples(ctx, c.EventID, cursor, vpnBatchSize)
		if err != nil {
			return err
		}
		caughtUp = read < vpnBatchSize
		if read == 0 {
			break
		}
		if len(samples) > 0 {
			earliest := samples[0].Handshake
			for _, s := range samples[1:] {
				if s.Handshake.Before(earliest) {
					earliest = s.Handshake
				}
			}
			known, err := u.store.OpenVPNSessions(ctx, c.EventID, earliest.Add(-eventAnalyticsModel.VPNSessionGap))
			if err != nil {
				return err
			}
			changed, absorbed := eventAnalyticsModel.MergeVPNSessions(known, samples, u.newID)
			if err = u.store.SaveVPNSessions(ctx, c.EventID, changed, absorbed); err != nil {
				return err
			}
		}
		if err = u.store.SetVPNCursor(ctx, c.EventID, next, nil); err != nil {
			return err
		}
		cursor = next
	}
	if caughtUp && eventAnalyticsModel.Final(c.FinishedAt, now) {
		return u.store.SetVPNCursor(ctx, c.EventID, cursor, &now)
	}
	return nil
}
