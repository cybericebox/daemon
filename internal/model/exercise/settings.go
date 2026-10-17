package exerciseModel

import (
	"time"

	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
)

// Default archive limits, one set for import and export: the export refuses what the import would refuse, so an
// exported task imports again. One entry may be as large as one stored file (MEDIA_MAX_UPLOAD_BYTES, 512 MiB by
// default), so every attachment fits.
const (
	// DefaultArchiveBytes is the largest task archive (.zip) without device images; one that carries them may be
	// larger by one image archive (ArchiveLimits.WithImagesBytes).
	DefaultArchiveBytes int64 = 512 << 20
	// DefaultArchiveEntryBytes is one file unpacked.
	DefaultArchiveEntryBytes int64 = 512 << 20
	// DefaultArchiveUnpackedBytes is all files unpacked together.
	DefaultArchiveUnpackedBytes int64 = 512 << 20
	// DefaultArchiveEntries counts every zip entry, folders and manifest.json included.
	DefaultArchiveEntries = 1000
)

const mib = 1 << 20

// ArchiveLimits bound a task archive, read on import and written on export.
type ArchiveLimits struct {
	// ArchiveBytes is the .zip file itself, without device images.
	ArchiveBytes int64
	// EntryBytes is one file unpacked.
	EntryBytes int64
	// UnpackedBytes is all files unpacked together.
	UnpackedBytes int64
	// Entries counts every zip entry, folders and manifest.json included.
	Entries int
}

// ArchiveLimitFields are the archive limits editable in the platform settings; sizes are bytes.
var ArchiveLimitFields = []platformSettingsModel.FieldSpec{
	{Key: "ArchiveBytes", Kind: platformSettingsModel.KindSize, Min: mib, Max: 2048 * mib},
	{Key: "EntryBytes", Kind: platformSettingsModel.KindSize, Min: mib, Max: 2048 * mib},
	{Key: "UnpackedBytes", Kind: platformSettingsModel.KindSize, Min: mib, Max: 2048 * mib},
	{Key: "Entries", Kind: platformSettingsModel.KindCount, Min: 10, Max: 10000},
}

// DefaultArchiveLimits are the limits in force while none are saved.
func DefaultArchiveLimits() ArchiveLimits {
	return ArchiveLimits{
		ArchiveBytes:  DefaultArchiveBytes,
		EntryBytes:    DefaultArchiveEntryBytes,
		UnpackedBytes: DefaultArchiveUnpackedBytes,
		Entries:       DefaultArchiveEntries,
	}
}

// WithImagesBytes is the largest archive that carries device images: the archive limit plus one image archive's.
func (l ArchiveLimits) WithImagesBytes() int64 {
	return l.ArchiveBytes + imageRegistryModel.MaxImageArchiveBytes
}

// Values are the limits keyed as ArchiveLimitFields.
func (l ArchiveLimits) Values() platformSettingsModel.Values {
	return platformSettingsModel.Values{
		"ArchiveBytes":  l.ArchiveBytes,
		"EntryBytes":    l.EntryBytes,
		"UnpackedBytes": l.UnpackedBytes,
		"Entries":       int64(l.Entries),
	}
}

// WithValues returns the limits of v, refusing values outside ArchiveLimitFields' bounds and one entry larger than
// all files together.
func (l ArchiveLimits) WithValues(v platformSettingsModel.Values) (ArchiveLimits, error) {
	if err := platformSettingsModel.ValidateValues(ArchiveLimitFields, v); err != nil {
		return ArchiveLimits{}, err
	}
	l = ArchiveLimits{ArchiveBytes: v["ArchiveBytes"], EntryBytes: v["EntryBytes"], UnpackedBytes: v["UnpackedBytes"], Entries: int(v["Entries"])}
	if l.EntryBytes > l.UnpackedBytes {
		return ArchiveLimits{}, platformSettingsModel.ErrArchiveEntryOverUnpacked.WithPublicContext("field", "EntryBytes").Err()
	}
	return l, nil
}

// TestRunLeases are how long a test deployment lives: Lease on start and each extension, Max at most in total.
type TestRunLeases struct {
	Lease time.Duration
	Max   time.Duration
}

// TestRunFields are the test run leases editable in the platform settings; values are seconds.
var TestRunFields = []platformSettingsModel.FieldSpec{
	{Key: "Lease", Kind: platformSettingsModel.KindDuration, Min: int64(time.Hour / time.Second), Max: int64(24 * time.Hour / time.Second)},
	{Key: "LeaseMax", Kind: platformSettingsModel.KindDuration, Min: int64(time.Hour / time.Second), Max: int64(168 * time.Hour / time.Second)},
}

// Values are the leases in seconds, keyed as TestRunFields.
func (l TestRunLeases) Values() platformSettingsModel.Values {
	return platformSettingsModel.Values{
		"Lease":    int64(l.Lease / time.Second),
		"LeaseMax": int64(l.Max / time.Second),
	}
}

// WithValues returns the leases of v (seconds), refusing values outside TestRunFields' bounds and a total under one
// lease.
func (l TestRunLeases) WithValues(v platformSettingsModel.Values) (TestRunLeases, error) {
	if err := platformSettingsModel.ValidateValues(TestRunFields, v); err != nil {
		return TestRunLeases{}, err
	}
	l = TestRunLeases{Lease: time.Duration(v["Lease"]) * time.Second, Max: time.Duration(v["LeaseMax"]) * time.Second}
	if l.Max < l.Lease {
		return TestRunLeases{}, platformSettingsModel.ErrTestLeaseOverMax.WithPublicContext("field", "LeaseMax").Err()
	}
	return l, nil
}
