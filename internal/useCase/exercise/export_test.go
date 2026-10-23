package exercise

import (
	"testing"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
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

// DefaultLimits are the default archive limits, for the tests that read or write an archive under them.
func DefaultLimits() exerciseModel.ArchiveLimits { return exerciseModel.DefaultArchiveLimits() }

// Limits are small archive limits (archive, one entry, all entries unpacked, entries), so a test needs no hundreds
// of MiB.
func Limits(archive, entry, unpacked int64, entries int) exerciseModel.ArchiveLimits {
	return exerciseModel.ArchiveLimits{ArchiveBytes: archive, EntryBytes: entry, UnpackedBytes: unpacked, Entries: entries}
}

// SetImportStateRetryDelay shortens the wait between writes of an import's outcome.
func SetImportStateRetryDelay(t testing.TB, d time.Duration) {
	old := importStateRetryDelay
	importStateRetryDelay = d
	t.Cleanup(func() { importStateRetryDelay = old })
}

// SecretValueGroups lets the external tests group the secrets of stored variants like a read does.
func SecretValueGroups(u *ExerciseUseCase, variants []exerciseModel.Variant) [][][]int {
	return u.secretValueGroups(variants)
}
