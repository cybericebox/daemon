package imageRegistryModel

import (
	"encoding/json"
	"time"
)

// CheckState is the last conclusive verdict on a registry credential.
type CheckState string

const (
	// CheckUnknown: never checked with a verdict yet.
	CheckUnknown CheckState = ""
	// CheckOK: the registry accepted the credential.
	CheckOK CheckState = "ok"
	// CheckBroken: the registry refused the credential (401/403: revoked, expired, wrong).
	CheckBroken CheckState = "broken"
)

// Transition says whether a check changed the state in a way people are told about.
type Transition int

const (
	TransitionNone Transition = iota
	// TransitionBroke: the credential just stopped working (also the first check that finds it refused).
	TransitionBroke
	// TransitionRestored: a broken credential works again.
	TransitionRestored
)

// Outcome is what one check attempt found. Only an auth refusal counts as broken; a provider 5xx, 429, a
// timeout or a network error says nothing about the credential, so the previous state stays.
type Outcome struct {
	conclusive bool
	working    bool
	reason     Reason
}

// Working: the registry accepted the credential.
func Working() Outcome { return Outcome{conclusive: true, working: true} }

// Refused: the registry refused the credential.
func Refused(reason Reason) Outcome { return Outcome{conclusive: true, reason: reason} }

// Inconclusive: the attempt failed for a reason that is not the credential's; it will be retried.
func Inconclusive(reason Reason) Outcome { return Outcome{reason: reason} }

// Check is the last result of checking one credential. It carries a reason code, never a token.
type Check struct {
	State CheckState `json:"state,omitempty"`
	// CheckedAt is when State was last concluded (zero: never).
	CheckedAt time.Time `json:"checkedAt,omitempty"`
	// Reason says why the credential is broken; empty otherwise.
	Reason Reason `json:"reason,omitempty"`
	// AttemptedAt is the last attempt, conclusive or not.
	AttemptedAt time.Time `json:"attemptedAt,omitempty"`
	// Failure is why the last attempt gave no verdict (it will be retried); empty when it did.
	Failure Reason `json:"failure,omitempty"`
}

// Apply records one attempt and reports the transition to tell people about.
func (c *Check) Apply(o Outcome, now time.Time) Transition {
	c.AttemptedAt = now
	if !o.conclusive {
		c.Failure = o.reason
		return TransitionNone
	}
	prev := c.State
	c.Failure, c.CheckedAt = "", now
	if o.working {
		c.State, c.Reason = CheckOK, ""
		if prev == CheckBroken {
			return TransitionRestored
		}
		return TransitionNone
	}
	c.State, c.Reason = CheckBroken, o.reason
	if prev != CheckBroken {
		return TransitionBroke
	}
	return TransitionNone
}

func (c Check) Marshal() ([]byte, error) { return json.Marshal(c) }

// ParseCheck reads a stored check. A missing or unreadable value is the unknown state: stored rows are forgiven.
func ParseCheck(raw []byte) (Check, error) {
	var c Check
	if len(raw) == 0 {
		return Check{}, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return Check{}, err
	}
	return c, nil
}
