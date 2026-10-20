// Package participantPreviewModel owns progress separate from competition attempts.
package participantPreviewModel

import (
	"time"

	"github.com/cybericebox/daemon/internal/model/challengeAttempt"
	"github.com/gofrs/uuid"
)

const SettingKey = "participant_preview_exercises"

type Selection struct {
	ID                uuid.UUID
	ExerciseVersionID uuid.UUID
	VariantID         uuid.UUID
}
type Run struct {
	ID, EventID, UserID, SelectionID uuid.UUID
	DeployID                         uuid.NullUUID
	CreatedAt, ExpiresAt             time.Time
	Attempts                         map[string]int                  `json:"attempts"`
	CorrectAttempts                  map[string]int                  `json:"correct_attempts"`
	Flags                            map[string]string               `json:"flags"`
	Solved                           map[string]time.Time            `json:"solved"`
	Hints                            map[string]map[string]time.Time `json:"hints"`
}

func NewRun(event, user, selection uuid.UUID, now time.Time) Run {
	return Run{ID: uuid.Must(uuid.NewV7()), EventID: event, UserID: user, SelectionID: selection, CreatedAt: now, ExpiresAt: now.Add(2 * time.Hour), Flags: map[string]string{}, Solved: map[string]time.Time{}, Hints: map[string]map[string]time.Time{}}
}
func (r *Run) Check(task uuid.UUID, flag string, now time.Time) (bool, bool) {
	expected, ok := r.Flags[task.String()]
	if !ok || !r.ExpiresAt.After(now) || !challengeAttempt.FlagMatches(expected, flag) {
		return false, false
	}
	return true, r.MarkSolved(task, now)
}
func (r *Run) MarkSolved(task uuid.UUID, now time.Time) bool {
	if r.Solved == nil {
		r.Solved = map[string]time.Time{}
	}
	if _, ok := r.Solved[task.String()]; ok {
		return false
	}
	r.Solved[task.String()] = now
	return true
}
func (r *Run) UnlockHint(task, hint uuid.UUID, now time.Time) {
	if r.Hints == nil {
		r.Hints = map[string]map[string]time.Time{}
	}
	if r.Hints[task.String()] == nil {
		r.Hints[task.String()] = map[string]time.Time{}
	}
	if _, ok := r.Hints[task.String()][hint.String()]; !ok {
		r.Hints[task.String()][hint.String()] = now
	}
}

func (r *Run) RecordAttempt(task uuid.UUID, correct bool) {
	if r.Attempts == nil {
		r.Attempts = map[string]int{}
	}
	if r.CorrectAttempts == nil {
		r.CorrectAttempts = map[string]int{}
	}
	r.Attempts[task.String()]++
	if correct {
		r.CorrectAttempts[task.String()]++
	}
}

type Settings struct {
	Selections []Selection
	Inherit    bool
	GroupName  *string
}
type Defaults struct {
	GroupName  string
	Selections []Selection
}
