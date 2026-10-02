package eventAnalytics

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	labTraffic "github.com/cybericebox/daemon/internal/model/labTraffic"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// isPlatformAdmin reports whether the caller is platform staff who may write
// events (admin, super_admin). A dismissal with the exercise scope outlives the
// event: it silences the signal in EVERY event that uses the catalog exercise,
// so it is not the business of one event's manager.
func isPlatformAdmin(ctx context.Context) bool {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx)
	return ok && claims.Role.HasPermission(rbac.PermEventsWrite)
}

// IntegrityStore is the statement port of «Доброчесність».
type IntegrityStore interface {
	IntegrityFacts(ctx context.Context, eventID uuid.UUID) (eventAnalyticsModel.IntegrityFacts, error)
	SolveReviews(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.SolveReview, error)
	SaveSolveReview(ctx context.Context, eventID, teamChallengeID, reviewer uuid.UUID, note string, at time.Time) (bool, error)
	DeleteSolveReview(ctx context.Context, eventID, teamChallengeID uuid.UUID) (bool, error)
	Dismissals(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.IntegrityDismissal, error)
	SaveDismissal(ctx context.Context, eventID, teamChallengeID uuid.UUID, scope eventAnalyticsModel.DismissScope, kind eventAnalyticsModel.IntegrityKind, key, note string, by, id uuid.UUID, at time.Time) (bool, error)
	DeleteDismissal(ctx context.Context, eventID, id uuid.UUID) (bool, error)
}

// LabTraffic answers «did the team touch the task's lab before X»
// (docs/LAB-TRAFFIC-ACCOUNTING.md).
type LabTraffic interface {
	Ask(ctx context.Context, q labTraffic.Question) (labTraffic.Answer, error)
}

const (
	// maxLabAsks bounds the lab questions of one report; the rest keep the
	// VPN-session fallback.
	maxLabAsks    = 500
	labAskWorkers = 8
)

// ReviewedFilter narrows the list by the organizer's decision.
type ReviewedFilter string

const (
	ReviewedAny    ReviewedFilter = ""
	ReviewedOnly   ReviewedFilter = "yes"
	ReviewedNotYet ReviewedFilter = "no"
)

// IntegrityFilter narrows the list of flagged solves. A nil ID or an empty
// value means no filter.
type IntegrityFilter struct {
	Signal      eventAnalyticsModel.IntegrityKind
	TeamID      uuid.UUID
	ChallengeID uuid.UUID
	Reviewed    ReviewedFilter
}

type (
	// SolveReviewView is a stored «перевірено» note.
	SolveReviewView struct {
		Note       string
		ReviewedBy string
		ReviewedAt time.Time
	}

	// IntegrityItem is a flagged solve with the organizer's decision.
	IntegrityItem struct {
		eventAnalyticsModel.FlaggedSolve
		Review *SolveReviewView
	}

	// IntegrityView is the «Доброчесність» report (§6.6): the flagged solves
	// to review. It is sensitive (other teams' names), so callers gate it
	// with LevelSensitive.
	IntegrityView struct {
		Items []IntegrityItem
		// Total counts the items matching the filter, before the response cap.
		Total int
		// Counts: flagged solves per signal kind among those matching the
		// filter apart from the signal, so chips show what a click yields.
		Counts map[eventAnalyticsModel.IntegrityKind]int
		// Thresholds are the effective values (the requested ones, clamped);
		// Defaults are the platform defaults.
		Thresholds eventAnalyticsModel.IntegrityThresholds
		Defaults   eventAnalyticsModel.IntegrityThresholds
		Period     PeriodView
	}

	// IntegrityFlag marks a solve for the attempts journal: no evidence,
	// no other team's data.
	IntegrityFlag struct {
		TeamChallengeID uuid.UUID
		TeamID          uuid.UUID
		ChallengeID     uuid.UUID
		Signals         []eventAnalyticsModel.IntegrityKind
		// CrossFlagTimes: the submissions of another team's flag, so the
		// journal marks those very attempts.
		CrossFlagTimes []time.Time
	}

	// DismissalView is a stored «не підсвічувати такі випадки».
	DismissalView struct {
		ID            uuid.UUID
		Scope         eventAnalyticsModel.DismissScope
		Kind          eventAnalyticsModel.IntegrityKind
		Key           string
		Note          string
		ChallengeName string
		CreatedBy     string
		CreatedAt     time.Time
	}
)

// integrityReport is the cached part: the flagged solves of a period. The
// organizer's decisions are read fresh, so a review shows at once.
type integrityReport struct {
	flagged []eventAnalyticsModel.FlaggedSolve
	period  PeriodView
}

func (u *EventAnalyticsUseCase) integrityReport(ctx context.Context, eventID uuid.UUID, from, to *time.Time, th eventAnalyticsModel.IntegrityThresholds) (integrityReport, error) {
	e, err := u.event(ctx, eventID)
	if err != nil {
		return integrityReport{}, err
	}
	period, err := eventAnalyticsModel.NewPeriod(from, to, e.Lifecycle.StartAt, e.Lifecycle.EffectiveFinishAt(), u.now())
	if err != nil {
		return integrityReport{}, err
	}
	var floors strings.Builder
	for _, level := range eventAnalyticsModel.IntegrityLevels {
		fmt.Fprintf(&floors, "%d,", th.Floors[level]/time.Second)
	}
	key := fmt.Sprintf("integrity:%s:%d:%d:%s%d:%d:%d", eventID, period.From.Unix(), period.To.Unix(), floors.String(),
		th.BruteForceAttempts, th.BruteForceWindow/time.Second, th.FollowGap/time.Second)
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (integrityReport, error) {
		facts, err := u.store.IntegrityFacts(ctx, eventID)
		if err != nil {
			return integrityReport{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read event integrity facts").Err()
		}
		u.askLabs(ctx, eventID, &facts)
		return integrityReport{
			flagged: eventAnalyticsModel.DetectIntegrity(facts, th, period.From, period.To, u.now()),
			period:  PeriodView{From: period.From, To: period.To},
		}, nil
	})
}

// applyDismissals removes what the organizers dismissed (fresh, over the
// cached detection).
func (u *EventAnalyticsUseCase) applyDismissals(ctx context.Context, eventID uuid.UUID, flagged []eventAnalyticsModel.FlaggedSolve) ([]eventAnalyticsModel.FlaggedSolve, error) {
	stored, err := u.store.Dismissals(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read integrity dismissals").Err()
	}
	dismissals := make([]eventAnalyticsModel.Dismissal, 0, len(stored))
	for _, d := range stored {
		dismissals = append(dismissals, eventAnalyticsModel.Dismissal{ExerciseID: d.ExerciseID, TaskID: d.TaskID, Kind: d.Kind, Key: d.Key})
	}
	return eventAnalyticsModel.ApplyDismissals(flagged, dismissals), nil
}

// askLabs puts the lab-traffic verdict on every solve of a lab task. A failed
// or missing answer leaves the solve without a verdict, so the detector falls
// back on the VPN sessions: no data never turns into «untouched».
func (u *EventAnalyticsUseCase) askLabs(ctx context.Context, eventID uuid.UUID, facts *eventAnalyticsModel.IntegrityFacts) {
	if u.labTraffic == nil {
		return
	}
	var wanted []int
	for i, s := range facts.Solves {
		if s.HasLab && len(wanted) < maxLabAsks {
			wanted = append(wanted, i)
		}
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < min(labAskWorkers, len(wanted)); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				s := &facts.Solves[i]
				answer, err := u.labTraffic.Ask(ctx, labTraffic.Question{
					EventID: eventID, TeamID: s.TeamID, EventChallengeID: s.ChallengeID, Before: s.SolvedAt,
				})
				if err != nil {
					continue
				}
				switch answer.Verdict {
				case labTraffic.Touched:
					s.Lab = eventAnalyticsModel.LabTouched
				case labTraffic.Untouched:
					s.Lab, s.LabAttempted, s.LabFirstSeen = eventAnalyticsModel.LabUntouched, answer.Attempted, answer.FirstSeenAt
				}
			}
		}()
	}
	for _, i := range wanted {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
}

func (u *EventAnalyticsUseCase) reviewIndex(ctx context.Context, eventID uuid.UUID) (map[uuid.UUID]eventAnalyticsRepo.SolveReview, error) {
	reviews, err := u.store.SolveReviews(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read solve reviews").Err()
	}
	index := make(map[uuid.UUID]eventAnalyticsRepo.SolveReview, len(reviews))
	for _, r := range reviews {
		index[r.TeamChallengeID] = r
	}
	return index, nil
}

// GetEventAnalyticsIntegrity lists the flagged solves of the period (nil
// bounds: the event's own window), most signals first, narrowed by filter.
func (u *EventAnalyticsUseCase) GetEventAnalyticsIntegrity(ctx context.Context, eventID uuid.UUID, from, to *time.Time, th eventAnalyticsModel.IntegrityThresholds, filter IntegrityFilter) (IntegrityView, error) {
	th = th.Clamped()
	report, err := u.integrityReport(ctx, eventID, from, to, th)
	if err != nil {
		return IntegrityView{}, err
	}
	reviews, err := u.reviewIndex(ctx, eventID)
	if err != nil {
		return IntegrityView{}, err
	}
	view := IntegrityView{
		Items:  []IntegrityItem{},
		Counts: map[eventAnalyticsModel.IntegrityKind]int{}, Thresholds: th, Defaults: eventAnalyticsModel.DefaultIntegrityThresholds(),
		Period: report.period,
	}
	flagged, err := u.applyDismissals(ctx, eventID, report.flagged)
	if err != nil {
		return IntegrityView{}, err
	}
	for _, f := range flagged {
		review, reviewed := reviews[f.TeamChallengeID]
		if !filter.matchesExceptSignal(f, reviewed) {
			continue
		}
		for _, s := range f.Signals {
			view.Counts[s.Kind]++
		}
		if filter.Signal != "" && !hasSignal(f, filter.Signal) {
			continue
		}
		view.Total++
		if len(view.Items) >= eventAnalyticsModel.MaxIntegrityItems {
			continue
		}
		item := IntegrityItem{FlaggedSolve: f}
		if reviewed {
			item.Review = &SolveReviewView{Note: review.Note, ReviewedBy: review.ReviewedByName, ReviewedAt: review.ReviewedAt}
		}
		view.Items = append(view.Items, item)
	}
	return view, nil
}

func (f IntegrityFilter) matchesExceptSignal(s eventAnalyticsModel.FlaggedSolve, reviewed bool) bool {
	switch {
	case f.TeamID != uuid.Nil && s.Team.ID != f.TeamID,
		f.ChallengeID != uuid.Nil && s.ChallengeID != f.ChallengeID,
		f.Reviewed == ReviewedOnly && !reviewed,
		f.Reviewed == ReviewedNotYet && reviewed:
		return false
	}
	return true
}

func hasSignal(s eventAnalyticsModel.FlaggedSolve, kind eventAnalyticsModel.IntegrityKind) bool {
	for _, x := range s.Signals {
		if x.Kind == kind {
			return true
		}
	}
	return false
}

// GetEventAnalyticsIntegrityFlags lists the unreviewed flagged solves of the
// whole event for the attempts journal. Sensitive, like the report.
func (u *EventAnalyticsUseCase) GetEventAnalyticsIntegrityFlags(ctx context.Context, eventID uuid.UUID) ([]IntegrityFlag, error) {
	report, err := u.integrityReport(ctx, eventID, nil, nil, eventAnalyticsModel.DefaultIntegrityThresholds().Clamped())
	if err != nil {
		return nil, err
	}
	reviews, err := u.reviewIndex(ctx, eventID)
	if err != nil {
		return nil, err
	}
	flagged, err := u.applyDismissals(ctx, eventID, report.flagged)
	if err != nil {
		return nil, err
	}
	flags := make([]IntegrityFlag, 0, len(flagged))
	for _, f := range flagged {
		if _, reviewed := reviews[f.TeamChallengeID]; reviewed {
			continue
		}
		flag := IntegrityFlag{TeamChallengeID: f.TeamChallengeID, TeamID: f.Team.ID, ChallengeID: f.ChallengeID}
		for _, s := range f.Signals {
			flag.Signals = append(flag.Signals, s.Kind)
			if s.Kind == eventAnalyticsModel.IntegrityCrossFlag {
				flag.CrossFlagTimes = s.Times
			}
		}
		flags = append(flags, flag)
	}
	return flags, nil
}

// ReviewEventSolve stores the organizer's «перевірено» note of a solve.
func (u *EventAnalyticsUseCase) ReviewEventSolve(ctx context.Context, eventID, teamChallengeID, reviewer uuid.UUID, note string) error {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > eventAnalyticsModel.MaxReviewNoteLength {
		return eventAnalyticsModel.ErrEventAnalyticsReviewNoteTooLong.Err()
	}
	if _, err := u.event(ctx, eventID); err != nil {
		return err
	}
	saved, err := u.store.SaveSolveReview(ctx, eventID, teamChallengeID, reviewer, note, u.now())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save the solve review").Err()
	}
	if !saved {
		return eventAnalyticsModel.ErrEventAnalyticsSolveNotFound.Err()
	}
	return nil
}

// UnreviewEventSolve removes the note; a solve without one is not found.
func (u *EventAnalyticsUseCase) UnreviewEventSolve(ctx context.Context, eventID, teamChallengeID uuid.UUID) error {
	if _, err := u.event(ctx, eventID); err != nil {
		return err
	}
	deleted, err := u.store.DeleteSolveReview(ctx, eventID, teamChallengeID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove the solve review").Err()
	}
	if !deleted {
		return eventAnalyticsModel.ErrEventAnalyticsSolveNotFound.Err()
	}
	return nil
}

// DismissIntegrityPattern stores «не підсвічувати такі випадки» for the
// catalog task behind a team task of the event: the signal kind plus its key
// (for shared_wrong the wrong value, otherwise none), for this event or for
// every event that uses the catalog exercise (the sensitive-access route gate applies: owner, write moderators, platform admins).
func (u *EventAnalyticsUseCase) DismissIntegrityPattern(ctx context.Context, eventID, teamChallengeID, by uuid.UUID, scope eventAnalyticsModel.DismissScope, kind eventAnalyticsModel.IntegrityKind, key, note string) error {
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > eventAnalyticsModel.MaxReviewNoteLength {
		return eventAnalyticsModel.ErrEventAnalyticsReviewNoteTooLong.Err()
	}
	if scope != eventAnalyticsModel.DismissEvent && scope != eventAnalyticsModel.DismissExercise {
		return eventAnalyticsModel.ErrEventAnalyticsDismissalInvalid.Err()
	}
	if scope == eventAnalyticsModel.DismissExercise && !isPlatformAdmin(ctx) {
		return authModel.ErrInsufficientPermission.Err()
	}
	if kind == eventAnalyticsModel.IntegritySharedWrong {
		key = eventAnalyticsModel.NormalizeAnswer(key)
		if key == "" || utf8.RuneCountInString(key) > 200 {
			return eventAnalyticsModel.ErrEventAnalyticsDismissalInvalid.Err()
		}
	} else {
		key = ""
	}
	if !kind.Dismissible() {
		return eventAnalyticsModel.ErrEventAnalyticsDismissalInvalid.Err()
	}
	if _, err := u.event(ctx, eventID); err != nil {
		return err
	}
	saved, err := u.store.SaveDismissal(ctx, eventID, teamChallengeID, scope, kind, key, note, by, uuid.Must(uuid.NewV7()), u.now())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save the integrity dismissal").Err()
	}
	if !saved {
		return eventAnalyticsModel.ErrEventAnalyticsSolveNotFound.Err()
	}
	return nil
}

// ListIntegrityDismissals lists the dismissals that apply to the event.
func (u *EventAnalyticsUseCase) ListIntegrityDismissals(ctx context.Context, eventID uuid.UUID) ([]DismissalView, error) {
	if _, err := u.event(ctx, eventID); err != nil {
		return nil, err
	}
	stored, err := u.store.Dismissals(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read integrity dismissals").Err()
	}
	admin := isPlatformAdmin(ctx)
	out := make([]DismissalView, 0, len(stored))
	for _, d := range stored {
		view := DismissalView{ID: d.ID, Scope: d.Scope, Kind: d.Kind, Key: d.Key, Note: d.Note,
			ChallengeName: d.ChallengeName, CreatedBy: d.CreatedByName, CreatedAt: d.CreatedAt}
		if d.Scope == eventAnalyticsModel.DismissExercise && !admin {
			// Written by another event's manager (or an admin): its author and
			// note are theirs, not this event's to read.
			view.CreatedBy, view.Note = "", ""
		}
		out = append(out, view)
	}
	return out, nil
}

// RemoveIntegrityDismissal takes a dismissal back; it is not found when it
// does not apply to the event.
func (u *EventAnalyticsUseCase) RemoveIntegrityDismissal(ctx context.Context, eventID, id uuid.UUID) error {
	if _, err := u.event(ctx, eventID); err != nil {
		return err
	}
	if !isPlatformAdmin(ctx) {
		// An exercise-scope dismissal belongs to every event that uses the
		// exercise: only platform admins take it back.
		stored, listErr := u.store.Dismissals(ctx, eventID)
		if listErr != nil {
			return model.ErrPlatform.WithError(listErr).WithMessage("Failed to read integrity dismissals").Err()
		}
		for _, d := range stored {
			if d.ID == id && d.Scope == eventAnalyticsModel.DismissExercise {
				return authModel.ErrInsufficientPermission.Err()
			}
		}
	}
	deleted, err := u.store.DeleteDismissal(ctx, eventID, id)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove the integrity dismissal").Err()
	}
	if !deleted {
		return eventAnalyticsModel.ErrEventAnalyticsSolveNotFound.Err()
	}
	return nil
}
