package event

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventResultRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/scoreboardRepo"
	"github.com/cybericebox/daemon/internal/model"
)

const liveResultsReplayLimit int32 = 500

// LiveResultChangeView is a revisioned, presentation-agnostic result update.
// Clients apply it only after first loading a regular results snapshot.
type LiveResultChangeView struct {
	Revision  int64           `json:"Revision"`
	Kind      string          `json:"Kind"`
	Payload   json.RawMessage `json:"Payload"`
	CreatedAt time.Time       `json:"CreatedAt"`
}

type LiveResultsReplay struct {
	SnapshotRequired bool                   `json:"SnapshotRequired"`
	Changes          []LiveResultChangeView `json:"Changes"`
	// LastRevision is the newest revision read, including changes skipped
	// for a frozen viewer; the stream continues after it.
	LastRevision int64 `json:"-"`
	// FreezeKey identifies the viewer's freeze state; when it changes during
	// a stream the client must reload its snapshot.
	FreezeKey string `json:"-"`
}

// liveViewerRefresh is how often a stream re-reads its viewer (manager,
// participant status, team); between refreshes a tick reads nothing per
// viewer.
const liveViewerRefresh = time.Minute

// LiveResultsStream is one viewer's live results subscription. It reads the
// viewer once (refreshed every liveViewerRefresh) and the event-wide part from
// the shared cache, so a tick of any number of streams of one event costs one
// database read per resultsSharedTTL.
type LiveResultsStream struct {
	u          *EventUseCase
	eventID    uuid.UUID
	access     ResultsAccess
	liveScreen bool
	viewer     resultsViewer
	viewerAt   time.Time
}

// LiveResultsSubscription replays the changes after a cursor, once per tick.
type LiveResultsSubscription interface {
	Replay(ctx context.Context, afterRevision int64) (LiveResultsReplay, error)
}

// OpenLiveResults starts a viewer's subscription; nothing is read until the
// first Replay.
func (u *EventUseCase) OpenLiveResults(eventID uuid.UUID, access ResultsAccess, liveScreen bool) LiveResultsSubscription {
	return &LiveResultsStream{u: u, eventID: eventID, access: access, liveScreen: liveScreen}
}

// ReplayLiveResults is a one-off Replay of a new subscription.
func (u *EventUseCase) ReplayLiveResults(ctx context.Context, eventID uuid.UUID, access ResultsAccess, afterRevision int64, liveScreen bool) (LiveResultsReplay, error) {
	return u.OpenLiveResults(eventID, access, liveScreen).Replay(ctx, afterRevision)
}

// Replay returns only changes made after the revision contained in a freshly
// loaded snapshot. It is intentionally not a recovery protocol for a previous
// broken connection: clients fetch a new snapshot before reopening SSE, while
// the first Replay covers the short race between snapshot and subscribe. A
// frozen viewer does not receive other teams' solves made after the freeze.
func (s *LiveResultsStream) Replay(ctx context.Context, afterRevision int64) (LiveResultsReplay, error) {
	now := time.Now()
	shared, err := s.u.sharedResults(ctx, s.eventID)
	if err != nil {
		return LiveResultsReplay{}, err
	}
	if s.viewerAt.IsZero() || now.Sub(s.viewerAt) >= liveViewerRefresh {
		s.viewer, s.viewerAt = s.u.loadResultsViewer(ctx, s.eventID, s.access), now
	}
	policy := shared.policy(s.viewer)
	if err = policy.readErr(now, s.liveScreen); err != nil {
		return LiveResultsReplay{}, err
	}
	freeze := policy.freeze(now, s.liveScreen)
	if afterRevision < 0 {
		return LiveResultsReplay{SnapshotRequired: true}, nil
	}
	changes, held := shared.changesAfter(afterRevision)
	if !held || afterRevision > shared.revision.Revision {
		// The cursor is older than the shared window (the snapshot raced a
		// commit) or newer than the shared revision (read on another
		// instance): answer from the database for this viewer only.
		if changes, err = s.u.changesAfterFromDB(ctx, s.eventID, afterRevision, shared.revision.Revision); err != nil {
			return LiveResultsReplay{}, err
		}
		if changes == nil {
			return LiveResultsReplay{SnapshotRequired: true}, nil
		}
	}
	replay := LiveResultsReplay{Changes: make([]LiveResultChangeView, 0, len(changes)), LastRevision: afterRevision, FreezeKey: freeze.freezeKey()}
	shown, err := s.shownTeams(ctx, policy, shared.revision.Revision, changes)
	if err != nil {
		return LiveResultsReplay{}, err
	}
	reload := false
	for _, change := range changes {
		replay.LastRevision = change.Revision
		if hiddenByFreeze(change, freeze.cutoff(), policy.ownTeam()) {
			continue
		}
		switch shown.verdict(change) {
		case changeDropped:
			continue
		case changeBelowTheRows:
			// A team below the shown rows may move into them: tell the client to reload instead of
			// handing it a row it does not display.
			reload = true
			continue
		}
		replay.Changes = append(replay.Changes, liveResultChangeView(change))
	}
	if reload {
		replay.Changes = append(replay.Changes, LiveResultChangeView{
			Revision: replay.LastRevision, Kind: string(eventResultRepo.ChangeScoreboardRecalculated), Payload: json.RawMessage(`{}`), CreatedAt: now.UTC(),
		})
	}
	return replay, nil
}

// shownTeams is who a viewer's board shows: the visible teams, the top RowsLimit of them, and the viewer's own
// team. A moderator sees every change; for anyone else a change about a team the board does not show is not
// sent (a hidden team's solve, a verdict on it, a team below the row limit).
type shownTeams struct {
	all     bool
	visible map[uuid.UUID]bool
	shown   map[uuid.UUID]bool
}

type changeVerdict int

const (
	changeKept changeVerdict = iota
	changeDropped
	changeBelowTheRows
)

// verdict decides one change. A change that names no team, or whose team cannot be read, is kept.
func (t shownTeams) verdict(change eventResultRepo.Change) changeVerdict {
	if t.all || (change.Kind != eventResultRepo.ChangeTeamChallengeSolved && change.Kind != eventResultRepo.ChangeTeamChallengeUnsolved) {
		return changeKept
	}
	var payload teamChallengeResultChange
	if err := json.Unmarshal(change.Payload, &payload); err != nil {
		return changeKept
	}
	switch {
	case t.shown[payload.TeamID]:
		return changeKept
	case t.visible[payload.TeamID]:
		return changeBelowTheRows
	}
	return changeDropped
}

func (s *LiveResultsStream) shownTeams(ctx context.Context, policy resultsPolicy, revision int64, changes []eventResultRepo.Change) (shownTeams, error) {
	if policy.manager || len(changes) == 0 {
		return shownTeams{all: true}, nil
	}
	own := policy.ownTeam()
	entries, err := revisionScores{u: s.u, revision: revision}.List(ctx, s.eventID, scoreboardRepo.Cut{IncludeTeam: own})
	if err != nil {
		return shownTeams{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the scoreboard").Err()
	}
	out := shownTeams{visible: make(map[uuid.UUID]bool, len(entries)), shown: make(map[uuid.UUID]bool, len(entries))}
	limit := len(entries)
	if rows := policy.cfg.Results.RowsLimit; rows != nil && int(*rows) < limit {
		limit = int(*rows)
	}
	for i, entry := range entries {
		out.visible[entry.TeamID] = true
		if i < limit || (own != nil && entry.TeamID == *own) {
			out.shown[entry.TeamID] = true
		}
	}
	return out, nil
}

// changesAfterFromDB reads the changes after a cursor the shared window cannot
// answer. nil means the cursor is unknown or expired: reload the snapshot.
func (u *EventUseCase) changesAfterFromDB(ctx context.Context, eventID uuid.UUID, afterRevision, sharedRevision int64) ([]eventResultRepo.Change, error) {
	current := eventResultRepo.Revision{Revision: sharedRevision}
	if afterRevision > current.Revision {
		// The shared revision may lag the one the snapshot was read at.
		var err error
		if current, err = u.results.CurrentRevision(ctx, eventID); err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event result revision").Err()
		}
		if afterRevision > current.Revision {
			return nil, nil
		}
	}
	earliest, err := u.results.EarliestChangeRevision(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get earliest result change").Err()
	}
	if afterRevision < current.Revision && (earliest == 0 || earliest > afterRevision+1) {
		return nil, nil
	}
	changes, err := u.results.ListChangesAfter(ctx, eventID, afterRevision, liveResultsReplayLimit)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list result changes").Err()
	}
	return append(make([]eventResultRepo.Change, 0, len(changes)), changes...), nil
}

// hiddenByFreeze reports another team's result change at or after the cutoff:
// a solve, or the withdrawal of one (an annulled solve is as much news about
// another team as the solve was).
func hiddenByFreeze(change eventResultRepo.Change, cutoff *time.Time, own *uuid.UUID) bool {
	if cutoff == nil {
		return false
	}
	var payload teamChallengeResultChange
	switch change.Kind {
	case eventResultRepo.ChangeTeamChallengeSolved:
		if err := json.Unmarshal(change.Payload, &payload); err != nil || payload.SolvedAt == nil {
			return false
		}
		return payload.SolvedAt.Compare(*cutoff) >= 0 && (own == nil || payload.TeamID != *own)
	case eventResultRepo.ChangeTeamChallengeUnsolved:
		// An unsolved change carries no solve time: it happened when it was recorded.
		if err := json.Unmarshal(change.Payload, &payload); err != nil {
			return true // unreadable: fail closed during a freeze
		}
		return change.CreatedAt.Compare(*cutoff) >= 0 && (own == nil || payload.TeamID != *own)
	}
	return false
}

func liveResultChangeView(change eventResultRepo.Change) LiveResultChangeView {
	return LiveResultChangeView{Revision: change.Revision, Kind: string(change.Kind), Payload: change.Payload, CreatedAt: change.CreatedAt}
}
