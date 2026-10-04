// Package eventChallengeModel owns a task as it appears on an event board.
// Source task content is copied into Snapshot so catalog updates cannot change
// a challenge already materialized for an event.
package eventChallengeModel

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

const defaultPoints int32 = 100

type EventChallenge struct {
	ID              uuid.UUID
	EventExerciseID uuid.UUID
	TaskID          uuid.UUID
	GroupID         *uuid.UUID
	Order           int32
	// BoardOrder is the place inside the group across the event's sets; nil
	// sorts after the ordered ones (by set attach time, then Order).
	BoardOrder      *int32
	Points          int32
	ScoringOverride *eventModel.ScoringProfile
	HintsEnabled    bool
	// MaxFlagAttempts overrides the event's limit of wrong submissions per team for this task; nil uses the event's.
	MaxFlagAttempts *int32
	Published       bool
	Snapshot        json.RawMessage
	// Hints is the canonical hint list of the task (text and level of the
	// canonical variant); HintCosts are the event's per-hint prices.
	Hints     []Hint
	HintCosts map[uuid.UUID]int32
	CreatedAt time.Time
}

// Hint is one unlockable hint as the event board knows it. Boards saved
// before levels carried a catalog "cost" here; it is ignored on decode (the
// migration moved it into HintCosts) and a missing level reads as nudge.
type Hint struct {
	ID    uuid.UUID               `json:"id"`
	Level exerciseModel.HintLevel `json:"level"`
	Text  string                  `json:"text"`
}

// UnmarshalJSON defaults a missing level to nudge (boards saved before levels).
func (h *Hint) UnmarshalJSON(data []byte) error {
	type plain Hint
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	if value.Level == "" {
		value.Level = exerciseModel.HintLevelNudge
	}
	*h = Hint(value)
	return nil
}

const hintCostMax int32 = 10000

// HintsForTask copies a task's hints (id, level, text).
func HintsForTask(task exerciseModel.Task) []Hint {
	out := make([]Hint, 0, len(task.Hints))
	for _, hint := range task.Hints {
		out = append(out, Hint{ID: hint.ID, Level: hint.Level, Text: hint.Text})
	}
	return out
}

// HintCost is the effective cost of one hint: the event's price, else free.
// found is false for an unknown hint.
func (c *EventChallenge) HintCost(hintID uuid.UUID) (cost int32, found bool) {
	for _, hint := range c.Hints {
		if hint.ID != hintID {
			continue
		}
		return c.HintCosts[hintID], true
	}
	return 0, false
}

// SetHintCosts replaces the event's overrides; a nil cost clears one. Every
// hint must belong to the challenge and costs stay within 0..10000.
func (c *EventChallenge) SetHintCosts(costs map[uuid.UUID]*int32) error {
	next := make(map[uuid.UUID]int32, len(costs))
	for id, cost := range costs {
		if _, found := c.HintCost(id); !found {
			return ErrEventChallengeHintCostsInvalid.Err()
		}
		if cost == nil {
			continue
		}
		if *cost < 0 || *cost > hintCostMax {
			return ErrEventChallengeHintCostsInvalid.Err()
		}
		next[id] = *cost
	}
	c.HintCosts = next
	return nil
}

// RefreshContent replaces the canonical snapshot and hints after a source
// switch; event overrides (points, publish, order, group, scoring, costs of
// hints that still exist) stay.
func (c *EventChallenge) RefreshContent(task exerciseModel.Task) error {
	snapshot, err := SnapshotForTask(task)
	if err != nil {
		return err
	}
	c.Snapshot = snapshot
	c.Hints = HintsForTask(task)
	kept := make(map[uuid.UUID]int32, len(c.HintCosts))
	for id, cost := range c.HintCosts {
		for _, hint := range c.Hints {
			if hint.ID == id {
				kept[id] = cost
			}
		}
	}
	c.HintCosts = kept
	return nil
}

// New copies participant-visible task data. Flags and per-variant secrets
// deliberately do not enter this event-level board snapshot.
func New(eventExerciseID uuid.UUID, task exerciseModel.Task, order int32, now time.Time) (EventChallenge, error) {
	if eventExerciseID == uuid.Nil || task.ID == uuid.Nil {
		return EventChallenge{}, ErrEventChallengeIdentityInvalid.Err()
	}
	snapshot, err := SnapshotForTask(task)
	if err != nil {
		return EventChallenge{}, err
	}
	return EventChallenge{ID: uuid.Must(uuid.NewV7()), EventExerciseID: eventExerciseID, TaskID: task.ID, Order: order, Points: defaultPoints, Snapshot: snapshot,
		Hints: HintsForTask(task), HintCosts: map[uuid.UUID]int32{}, CreatedAt: now}, nil
}

// SnapshotForTask copies only participant-visible task data. It is shared by
// event-board and team-level materialization so a variant-specific description
// is retained without ever exposing flags or topology secrets.
func SnapshotForTask(task exerciseModel.Task) (json.RawMessage, error) {
	snapshot, err := json.Marshal(struct {
		Name        string                        `json:"name"`
		Description json.RawMessage               `json:"description,omitempty"`
		Difficulty  exerciseModel.Difficulty      `json:"difficulty"`
		Attachments []exerciseModel.AttachmentRef `json:"attachments,omitempty"`
		// Placeholders let the participant view fill the description's inline
		// values (subnets, IPs, links) from the team's lab status.
		Placeholders []exerciseModel.Placeholder `json:"placeholders,omitempty"`
	}{Name: strings.TrimSpace(task.Name), Description: task.Description, Difficulty: task.Difficulty, Attachments: task.Attachments, Placeholders: task.Placeholders})
	if err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (c *EventChallenge) SetPoints(points int32) error {
	if points <= 0 {
		return ErrEventChallengePointsInvalid.Err()
	}
	c.Points = points
	return nil
}

func (c *EventChallenge) SetScoringOverride(profile *eventModel.ScoringProfile) {
	if profile == nil {
		c.ScoringOverride = nil
		return
	}
	copy := *profile
	c.ScoringOverride = &copy
}

func (c *EventChallenge) SetHintsEnabled(enabled bool) { c.HintsEnabled = enabled }

// SetMaxFlagAttempts sets (or clears with nil) the task's own attempt limit.
func (c *EventChallenge) SetMaxFlagAttempts(limit *int32) error {
	if err := challengeAttempt.CheckAttemptLimit(limit); err != nil {
		return err
	}
	if limit == nil {
		c.MaxFlagAttempts = nil
		return nil
	}
	value := *limit
	c.MaxFlagAttempts = &value
	return nil
}

func (c *EventChallenge) SetPublished(published bool) { c.Published = published }
