package exerciseModel_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

const mib = int64(1 << 20)

// publicField is the public context field of an application error.
func publicField(t *testing.T, e error) any {
	t.Helper()
	var ae appErr.Error
	if !errors.As(e, &ae) {
		t.Fatalf("not an application error: %v", e)
	}
	return ae.PublicContext()["field"]
}

func TestArchiveLimitFields_MatchTheSpec(t *testing.T) {
	size := platformSettingsModel.KindSize
	want := []platformSettingsModel.FieldSpec{
		{Key: "ArchiveBytes", Kind: size, Min: mib, Max: 1024 * mib},
		{Key: "EntryBytes", Kind: size, Min: mib, Max: 1024 * mib},
		{Key: "UnpackedBytes", Kind: size, Min: mib, Max: 1024 * mib},
		{Key: "Entries", Kind: platformSettingsModel.KindCount, Min: 10, Max: 10000},
	}
	if !reflect.DeepEqual(exerciseModel.ArchiveLimitFields, want) {
		t.Fatalf("fields:\n got %+v\nwant %+v", exerciseModel.ArchiveLimitFields, want)
	}
}

func TestDefaultArchiveLimits(t *testing.T) {
	l := exerciseModel.DefaultArchiveLimits()
	if l != (exerciseModel.ArchiveLimits{ArchiveBytes: 512 * mib, EntryBytes: 512 * mib, UnpackedBytes: 512 * mib, Entries: 1000}) {
		t.Fatalf("defaults: %+v", l)
	}
	if l.WithImagesBytes() != 512*mib+imageRegistryModel.MaxImageArchiveBytes {
		t.Fatalf("with images: %d", l.WithImagesBytes())
	}
	v := l.Values()
	if v["ArchiveBytes"] != 512*mib || v["Entries"] != 1000 || len(v) != 4 {
		t.Fatalf("values: %v", v)
	}
}

func TestArchiveLimits_WithValues(t *testing.T) {
	l := exerciseModel.DefaultArchiveLimits()
	got, err := l.WithValues(platformSettingsModel.Values{"ArchiveBytes": 2 * mib, "EntryBytes": 3 * mib, "UnpackedBytes": 3 * mib, "Entries": 10})
	if err != nil {
		t.Fatal(err)
	}
	if got != (exerciseModel.ArchiveLimits{ArchiveBytes: 2 * mib, EntryBytes: 3 * mib, UnpackedBytes: 3 * mib, Entries: 10}) {
		t.Fatalf("applied: %+v", got)
	}
	_, err = l.WithValues(platformSettingsModel.Values{"ArchiveBytes": 2 * mib, "EntryBytes": 4 * mib, "UnpackedBytes": 3 * mib, "Entries": 10})
	if !errors.Is(err, platformSettingsModel.ErrArchiveEntryOverUnpacked.Err()) {
		t.Fatalf("entry over unpacked: %v", err)
	}
	if field := publicField(t, err); field != "EntryBytes" {
		t.Fatalf("entry error field: %v", field)
	}
	// The import holds the zip and the unpacked files in memory: at most 1 GiB each.
	if _, err = l.WithValues(platformSettingsModel.Values{"ArchiveBytes": 1025 * mib, "EntryBytes": 3 * mib, "UnpackedBytes": 3 * mib, "Entries": 10}); !errors.Is(err, platformSettingsModel.ErrSettingValueOutOfRange.Err()) {
		t.Fatalf("ArchiveBytes over 1 GiB: %v", err)
	}
	if _, err = l.WithValues(platformSettingsModel.Values{"ArchiveBytes": 2 * mib, "EntryBytes": 3 * mib, "UnpackedBytes": 3 * mib, "Entries": 9}); !errors.Is(err, platformSettingsModel.ErrSettingValueOutOfRange.Err()) {
		t.Fatalf("entries under min: %v", err)
	}
}

func TestTestRunFields_MatchTheSpec(t *testing.T) {
	d := platformSettingsModel.KindDuration
	hour := int64(3600)
	want := []platformSettingsModel.FieldSpec{
		{Key: "Lease", Kind: d, Min: hour, Max: 8 * hour},
		{Key: "LeaseMax", Kind: d, Min: hour, Max: 168 * hour},
	}
	if !reflect.DeepEqual(exerciseModel.TestRunFields, want) {
		t.Fatalf("fields:\n got %+v\nwant %+v", exerciseModel.TestRunFields, want)
	}
}

func TestTestRunLeases_WithValues(t *testing.T) {
	l := exerciseModel.TestRunLeases{Lease: 2 * time.Hour, Max: 8 * time.Hour}
	if v := l.Values(); v["Lease"] != 7200 || v["LeaseMax"] != 28800 || len(v) != 2 {
		t.Fatalf("values: %v", v)
	}
	if got, err := l.WithValues(l.Values()); err != nil || got != l {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	got, err := l.WithValues(platformSettingsModel.Values{"Lease": 3600, "LeaseMax": 3600})
	if err != nil || got != (exerciseModel.TestRunLeases{Lease: time.Hour, Max: time.Hour}) {
		t.Fatalf("max equal to lease is accepted: %+v %v", got, err)
	}
	// One lease never exceeds what the calendar can book (8h).
	if _, err = l.WithValues(platformSettingsModel.Values{"Lease": 9 * 3600, "LeaseMax": 10 * 3600}); !errors.Is(err, platformSettingsModel.ErrSettingValueOutOfRange.Err()) {
		t.Fatalf("a 9h lease: %v", err)
	}
	_, err = l.WithValues(platformSettingsModel.Values{"Lease": 7200, "LeaseMax": 3600})
	if !errors.Is(err, platformSettingsModel.ErrTestLeaseOverMax.Err()) {
		t.Fatalf("max under lease: %v", err)
	}
	if field := publicField(t, err); field != "LeaseMax" {
		t.Fatalf("lease error field: %v", field)
	}
	if _, err = l.WithValues(platformSettingsModel.Values{"Lease": 25 * 3600, "LeaseMax": 100 * 3600}); !errors.Is(err, platformSettingsModel.ErrSettingValueOutOfRange.Err()) {
		t.Fatalf("lease over a day: %v", err)
	}
}
