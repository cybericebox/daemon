package exercise

import "time"

// EnvSecretContext lets the external tests seal a secret the way the use case does.
var EnvSecretContext = envSecretContext

// SetClock fixes the time of the lease checks.
func (u *ExerciseUseCase) SetClock(now func() time.Time) { u.clock = now }

// SameTestGroup exposes the group filter to the external tests.
var SameTestGroup = sameTestGroup
