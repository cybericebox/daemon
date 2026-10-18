package retentionModel_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// publicField is the public context field of an application error.
func publicField(t *testing.T, e error) any {
	t.Helper()
	var ae appErr.Error
	if !errors.As(e, &ae) {
		t.Fatalf("not an application error: %v", e)
	}
	return ae.PublicContext()["field"]
}

const daySeconds = int64(24 * 60 * 60)

func TestSettingsFields_MatchTheSpec(t *testing.T) {
	d := platformSettingsModel.KindDuration
	want := []platformSettingsModel.FieldSpec{
		{Key: "PendingAccount", Kind: d, Min: daySeconds, Max: 365 * daySeconds, InPrivacyPolicy: true},
		{Key: "InactiveAccount", Kind: d, Min: 180 * daySeconds, Max: 3650 * daySeconds, InPrivacyPolicy: true},
		{Key: "InactivityGrace", Kind: d, Min: 7 * daySeconds, Max: 365 * daySeconds},
		{Key: "ExpiredInvitation", Kind: d, Min: daySeconds, Max: 365 * daySeconds},
		{Key: "SessionAfterExpiry", Kind: d, Min: daySeconds, Max: 1825 * daySeconds, InPrivacyPolicy: true},
		{Key: "DeliveryLog", Kind: d, Min: daySeconds, Max: 1825 * daySeconds, InPrivacyPolicy: true},
		{Key: "SignalHistory", Kind: d, Min: daySeconds, Max: 1825 * daySeconds, InPrivacyPolicy: true},
		{Key: "FormAnswersAfterEventEnd", Kind: d, Min: daySeconds, Max: 1825 * daySeconds, InPrivacyPolicy: true},
		{Key: "EventAnalyticsAfterEnd", Kind: d, Min: daySeconds, Max: 1825 * daySeconds, InPrivacyPolicy: true},
		{Key: "LabTelemetry", Kind: d, Min: daySeconds, Max: 1825 * daySeconds, InPrivacyPolicy: true},
		{Key: "UnusedAnswerFile", Kind: d, Min: daySeconds, Max: 30 * daySeconds},
	}
	if !reflect.DeepEqual(retentionModel.SettingsFields, want) {
		t.Fatalf("fields:\n got %+v\nwant %+v", retentionModel.SettingsFields, want)
	}
}

func TestPolicy_ValuesAreSeconds(t *testing.T) {
	v := retentionModel.DefaultPolicy().Values()
	if len(v) != len(retentionModel.SettingsFields) {
		t.Fatalf("one value per field: %v", v)
	}
	if v["InactiveAccount"] != 3*365*daySeconds || v["UnusedAnswerFile"] != daySeconds || v["SessionAfterExpiry"] != 90*daySeconds {
		t.Fatalf("values: %v", v)
	}
}

func TestPolicy_WithValues_RoundTrip(t *testing.T) {
	p := retentionModel.DefaultPolicy()
	got, err := p.WithValues(p.Values())
	if err != nil {
		t.Fatalf("defaults must apply: %v", err)
	}
	if got != p {
		t.Fatalf("round trip:\n got %+v\nwant %+v", got, p)
	}
}

func TestPolicy_WithValues_SetsEveryPeriodAndKeepsBatchSize(t *testing.T) {
	p := retentionModel.DefaultPolicy()
	p.BatchSize = 42
	v := p.Values()
	v["DeliveryLog"] = 10 * daySeconds
	v["LabTelemetry"] = 11 * daySeconds
	v["UnusedAnswerFile"] = 2 * daySeconds
	got, err := p.WithValues(v)
	if err != nil {
		t.Fatal(err)
	}
	if got.BatchSize != 42 {
		t.Fatalf("batch size not kept: %d", got.BatchSize)
	}
	if got.DeliveryLog != 10*24*time.Hour || got.LabTelemetry != 11*24*time.Hour || got.UnusedAnswerFile != 48*time.Hour {
		t.Fatalf("periods not applied: %+v", got)
	}
}

func TestPolicy_WithValues_Refuses(t *testing.T) {
	p := retentionModel.DefaultPolicy()

	equal := p.Values()
	equal["InactiveAccount"] = 180 * daySeconds
	equal["InactivityGrace"] = 180 * daySeconds
	_, err := p.WithValues(equal)
	if !errors.Is(err, platformSettingsModel.ErrRetentionGraceNotShorter.Err()) {
		t.Fatalf("grace equal to inactivity: %v", err)
	}
	if field := publicField(t, err); field != "InactivityGrace" {
		t.Fatalf("grace error field: %v", field)
	}

	zero := p.Values()
	zero["DeliveryLog"] = 0
	if _, err := p.WithValues(zero); !errors.Is(err, platformSettingsModel.ErrSettingValueOutOfRange.Err()) {
		t.Fatalf("a zero period: %v", err)
	}

	missing := p.Values()
	delete(missing, "SignalHistory")
	if _, err := p.WithValues(missing); !errors.Is(err, platformSettingsModel.ErrSettingValueMissing.Err()) {
		t.Fatalf("a missing period: %v", err)
	}
}
