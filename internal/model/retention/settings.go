package retentionModel

import (
	"time"

	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
)

// SettingsFields are the periods editable in the platform settings, in the order the admin form shows them;
// values are seconds. InPrivacyPolicy marks the periods the Privacy Policy publishes.
var SettingsFields = []platformSettingsModel.FieldSpec{
	period("PendingAccount", day, 365*day, true),
	period("InactiveAccount", 180*day, 3650*day, true),
	period("InactivityGrace", 7*day, 365*day, false),
	period("ExpiredInvitation", day, 365*day, false),
	period("SessionAfterExpiry", day, 1825*day, true),
	period("DeliveryLog", day, 1825*day, true),
	period("SignalHistory", day, 1825*day, true),
	period("FormAnswersAfterEventEnd", day, 1825*day, true),
	period("EventAnalyticsAfterEnd", day, 1825*day, true),
	period("LabTelemetry", day, 1825*day, true),
	period("UnusedAnswerFile", day, 30*day, false),
}

func period(key string, minimum, maximum time.Duration, inPrivacyPolicy bool) platformSettingsModel.FieldSpec {
	return platformSettingsModel.FieldSpec{
		Key: key, Kind: platformSettingsModel.KindDuration,
		Min: int64(minimum / time.Second), Max: int64(maximum / time.Second), InPrivacyPolicy: inPrivacyPolicy,
	}
}

// periodsOf maps each settings field to its period in p. A free function, not a method: every Policy method has a
// value receiver.
func periodsOf(p *Policy) map[string]*time.Duration {
	return map[string]*time.Duration{
		"PendingAccount":           &p.PendingAccount,
		"InactiveAccount":          &p.InactiveAccount,
		"InactivityGrace":          &p.InactivityGrace,
		"ExpiredInvitation":        &p.ExpiredInvitation,
		"SessionAfterExpiry":       &p.SessionAfterExpiry,
		"DeliveryLog":              &p.DeliveryLog,
		"SignalHistory":            &p.SignalHistory,
		"FormAnswersAfterEventEnd": &p.FormAnswersAfterEventEnd,
		"EventAnalyticsAfterEnd":   &p.EventAnalyticsAfterEnd,
		"LabTelemetry":             &p.LabTelemetry,
		"UnusedAnswerFile":         &p.UnusedAnswerFile,
	}
}

// Values are the policy's periods in seconds, keyed as SettingsFields.
func (p Policy) Values() platformSettingsModel.Values {
	periods := periodsOf(&p)
	v := make(platformSettingsModel.Values, len(periods))
	for key, d := range periods {
		v[key] = int64(*d / time.Second)
	}
	return v
}

// WithValues returns the policy with the periods of v (seconds), refusing values outside SettingsFields' bounds and
// a deletion grace not shorter than the inactivity period. BatchSize is kept.
func (p Policy) WithValues(v platformSettingsModel.Values) (Policy, error) {
	if err := platformSettingsModel.ValidateValues(SettingsFields, v); err != nil {
		return Policy{}, err
	}
	for key, d := range periodsOf(&p) {
		*d = time.Duration(v[key]) * time.Second
	}
	if p.InactivityGrace >= p.InactiveAccount {
		return Policy{}, platformSettingsModel.ErrRetentionGraceNotShorter.WithPublicContext("field", "InactivityGrace").Err()
	}
	return p, nil
}
