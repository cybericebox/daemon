package exercise

import (
	"testing"
	"time"
)

// EnvSecretContext lets the external tests seal a secret the way the use case does.
var EnvSecretContext = envSecretContext

// SetClock fixes the time of the lease checks.
func (u *ExerciseUseCase) SetClock(now func() time.Time) { u.clock = now }

// SameTestGroup exposes the group filter to the external tests.
var SameTestGroup = sameTestGroup

// FileUse, AssignExportPaths and ExportName expose the export layout to the external tests.
type FileUse = fileUse

var (
	AssignExportPaths   = assignExportPaths
	ExportName          = exportName
	NormalizeContentIDs = normalizeContentIDs
)

// SetArchiveLimits runs one test under small archive limits (archive, one entry, all entries unpacked, entries).
func SetArchiveLimits(t testing.TB, archive, entry, unpacked int64, entries int) {
	old := limits
	limits = archiveLimits{Archive: archive, Entry: entry, Unpacked: unpacked, Entries: entries}
	t.Cleanup(func() { limits = old })
}
