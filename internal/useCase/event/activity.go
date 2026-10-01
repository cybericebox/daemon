package event

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
)

// activityWriteTimeout bounds one activity log write, which outlives the
// request that caused it.
const activityWriteTimeout = 5 * time.Second

// ActivityLog is the event activity log port (satisfied by
// *eventActivityRepo.Repository).
type ActivityLog interface {
	Append(ctx context.Context, a eventActivityModel.Activity) error
	AppendOnce(ctx context.Context, a eventActivityModel.Activity, window time.Duration) (bool, error)
}

// recordActivity appends to the activity log without ever failing the
// action it describes: the write is detached from the request and a failure
// is only logged.
func (u *EventUseCase) recordActivity(ctx context.Context, a eventActivityModel.Activity) {
	if u.activity == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), activityWriteTimeout)
	defer cancel()
	if err := u.activity.Append(writeCtx, a); err != nil {
		log.Warn().Err(err).Str("kind", string(a.Kind)).Stringer("event_id", a.EventID).Msg("event activity: write failed")
	}
}

// OpenOwnChallenge records that the participant opened a task of the team's
// board (time on task, engagement). At most one row per user and task per
// eventActivityModel.TaskOpenWindow: repeats inside the window return at
// once, without a database read.
func (u *EventUseCase) OpenOwnChallenge(ctx context.Context, eventID, userID, challengeID uuid.UUID) error {
	now := time.Now()
	key := taskOpenKey{userID: userID, challengeID: challengeID}
	if u.taskOpens.recent(key, now) {
		return nil
	}
	teamID, views, err := u.ownBoard(ctx, eventID, userID, false)
	if err != nil {
		return err
	}
	if _, found := boardChallenge(views, challengeID); !found {
		return eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	if u.activity != nil {
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), activityWriteTimeout)
		defer cancel()
		opened := eventActivityModel.TaskOpened(eventID, userID, teamID, challengeID, now)
		if _, err = u.activity.AppendOnce(writeCtx, opened, eventActivityModel.TaskOpenWindow); err != nil {
			log.Warn().Err(err).Stringer("event_id", eventID).Msg("event activity: task open write failed")
			return nil
		}
	}
	u.taskOpens.mark(key, now)
	return nil
}

// recordRejectedSubmission logs a submission refused before it became an
// attempt (D2), with the team the participant is in. Other failures are not
// refusals and are not logged.
func (u *EventUseCase) recordRejectedSubmission(ctx context.Context, eventID, userID, challengeID uuid.UUID, at time.Time, submitErr error) {
	if u.activity == nil {
		return
	}
	reason, ok := eventActivityModel.ClassifyRejection(submitErr, at, func() (eventModel.Lifecycle, bool) {
		e, err := u.events.GetByID(ctx, eventID)
		return e.Lifecycle, err == nil
	})
	if !ok {
		return
	}
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		// Someone who is not a participant has nothing to analyse.
		if !repositoryTools.IsObjectNotFoundError(err) {
			log.Warn().Err(err).Stringer("event_id", eventID).Msg("event activity: rejected submission not logged")
		}
		return
	}
	u.recordActivity(ctx, eventActivityModel.AttemptRejected(eventID, userID, p.TeamID, challengeID, reason, at))
}

type taskOpenKey struct{ userID, challengeID uuid.UUID }

// taskOpenThrottle remembers the task opens logged in this process within
// the dedupe window, so the open beacon costs no database work on repeats.
// The database dedupe still holds across replicas.
type taskOpenThrottle struct {
	mu     sync.Mutex
	window time.Duration
	seen   map[taskOpenKey]time.Time
}

// taskOpenThrottleSweep is the size at which expired entries are dropped.
const taskOpenThrottleSweep = 4096

func newTaskOpenThrottle(window time.Duration) *taskOpenThrottle {
	return &taskOpenThrottle{window: window, seen: map[taskOpenKey]time.Time{}}
}

func (t *taskOpenThrottle) recent(key taskOpenKey, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.seen[key]
	return ok && now.Sub(at) < t.window
}

func (t *taskOpenThrottle) mark(key taskOpenKey, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.seen) >= taskOpenThrottleSweep {
		for k, at := range t.seen {
			if now.Sub(at) >= t.window {
				delete(t.seen, k)
			}
		}
	}
	t.seen[key] = now
}
