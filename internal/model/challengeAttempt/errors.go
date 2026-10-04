package challengeAttempt

import (
	"net/http"

	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// EventChallengeObjectCode is shared with eventChallenge, teamChallenge and
// eventChallengeGroup; attempts use 20+ — next free detail code: 40.

var ErrAttemptInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Challenge attempt data is invalid").WithDetailCode(20)

var ErrDecisionInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Challenge attempt decision is invalid").WithDetailCode(21)

var ErrDecisionReasonRequired = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Challenge attempt decision reason is required").WithDetailCode(22)

var ErrAttemptNotFound = err.ErrObjectNotFound.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Challenge attempt not found").WithDetailCode(23)

var ErrIdempotencyKeyRequired = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Idempotency key is required").WithDetailCode(24)

var ErrIdempotencyKeyConflict = err.ErrConflict.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Idempotency key was already used for a different request").WithDetailCode(25)

var ErrIdempotencyInProgress = err.ErrConflict.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Idempotent request is still being processed").WithDetailCode(26)

var ErrAttemptCursorInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Solution attempt cursor is invalid").WithDetailCode(29)

// ErrNothingToAnnul: the team has no accepted attempt on the challenge.
var ErrNothingToAnnul = err.ErrConflict.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("The team has no accepted solve of this challenge to annul").WithDetailCode(30)

// ErrAnswerTooLong: the submitted answer is longer than a flag can be (FLAG_ANSWER_MAX_BYTES). It is refused
// before anything is stored: attempts are kept and shown in the journal and the exports.
var ErrAnswerTooLong = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("The answer is too long").WithDetailCode(37)

// ErrTooManyAttempts: the flag submission rate limit was hit (HTTP 429); the
// Retry-After header carries the wait in seconds.
var ErrTooManyAttempts = err.ErrConflict.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("Too many flag submissions, try again later").WithDetailCode(34).
	WithHTTPCode(http.StatusTooManyRequests)

// ErrAttemptLimitInvalid: a max-flag-attempts value is not a whole number from 1 to MaxAttemptLimit.
var ErrAttemptLimitInvalid = err.ErrInvalidData.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("The maximum number of flag attempts is invalid").WithDetailCode(38)

// ErrAttemptLimitReached: the team used all its wrong attempts on the task (HTTP 409). It is returned before any
// flag check, so the answer is neither compared nor stored.
var ErrAttemptLimitReached = err.ErrConflict.WithObjectCode(model.EventChallengeObjectCode).
	WithMessage("No flag attempts left for this task").WithDetailCode(39)
