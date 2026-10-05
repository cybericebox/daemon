package exercise

import "time"

// EnvSecretContext lets the external tests seal a secret the way the use case does.
var EnvSecretContext = envSecretContext

// SetGroupWait shortens the wait for a group that is still being deleted; it returns the restore func.
func SetGroupWait(max, start, ceiling time.Duration) func() {
	m, s, c := testGroupWaitMax, testGroupWaitStart, testGroupWaitCeiling
	testGroupWaitMax, testGroupWaitStart, testGroupWaitCeiling = max, start, ceiling
	return func() { testGroupWaitMax, testGroupWaitStart, testGroupWaitCeiling = m, s, c }
}
