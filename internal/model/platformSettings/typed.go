package platformSettingsModel

import (
	"maps"
	"slices"
)

// Typed settings: a stored row whose value is a fixed set of integer fields, each with bounds, edited on its own
// admin tab instead of the generic key/value list.

// Keys of the typed settings rows.
const (
	RetentionKey     = "retention_policy"
	ArchiveLimitsKey = "exercise_archive_limits"
	TestRunsKey      = "exercise_test_runs"
	NodeProfilesKey  = "node_profiles"
)

// Kind is how a field's integer is read: durations in seconds, sizes in bytes, counts as is.
type Kind string

const (
	KindDuration Kind = "duration"
	KindSize     Kind = "size"
	KindCount    Kind = "count"
)

type (
	// FieldSpec is one field of a typed setting and its inclusive bounds.
	FieldSpec struct {
		Key  string
		Kind Kind
		Min  int64
		Max  int64
		// InPrivacyPolicy marks a period published in the Privacy Policy.
		InPrivacyPolicy bool
	}

	// Values are a typed setting's fields by key.
	Values map[string]int64
)

// IsTypedKey tells whether key is a typed setting, never saved through the generic setting path.
func IsTypedKey(key string) bool {
	switch key {
	case RetentionKey, ArchiveLimitsKey, TestRunsKey, NodeProfilesKey:
		return true
	}
	return false
}

// ValidateValues accepts v only when it holds every field of specs, no other key, and each value within its bounds.
// Of several unknown keys the first in sorted order is reported, so the answer does not depend on map order.
func ValidateValues(specs []FieldSpec, v Values) error {
	known := make(map[string]bool, len(specs))
	for _, spec := range specs {
		known[spec.Key] = true
	}
	for _, key := range slices.Sorted(maps.Keys(v)) {
		if !known[key] {
			return ErrSettingUnknownField.WithPublicContext("field", key).Err()
		}
	}
	for _, spec := range specs {
		value, ok := v[spec.Key]
		if !ok {
			return ErrSettingValueMissing.WithPublicContext("field", spec.Key).Err()
		}
		if value < spec.Min || value > spec.Max {
			return ErrSettingValueOutOfRange.WithPublicContext("field", spec.Key).
				WithPublicContext("min", spec.Min).WithPublicContext("max", spec.Max).Err()
		}
	}
	return nil
}

// Overlay returns base with the stored values of the fields of specs laid over it; keys outside specs are dropped.
// It does not validate: the caller validates the result.
func Overlay(base, stored Values, specs []FieldSpec) Values {
	out := make(Values, len(specs))
	for _, spec := range specs {
		if value, ok := stored[spec.Key]; ok {
			out[spec.Key] = value
		} else if value, ok = base[spec.Key]; ok {
			out[spec.Key] = value
		}
	}
	return out
}

// Description is a setting's human-readable explanation in each interface language.
type Description struct {
	Uk string `json:"Uk"`
	En string `json:"En"`
}

// descriptions explain the settings the platform itself reads, so the generic list can show words instead of
// the raw key. A key that is not here has no description.
var descriptions = map[string]Description{
	RetentionKey: {
		Uk: "Терміни зберігання даних: сеанси, телеметрія лабораторій і учасників, журнали та інше.",
		En: "Data retention periods: sessions, lab and participant telemetry, logs and more.",
	},
	ArchiveLimitsKey: {
		Uk: "Обмеження на розмір і вміст архівів завдань, що імпортуються.",
		En: "Size and content limits for imported task archives.",
	},
	TestRunsKey: {
		Uk: "Обмеження тестових запусків завдань: тривалість, кількість і ресурси.",
		En: "Limits for task test runs: duration, count and resources.",
	},
	NodeProfilesKey: {
		Uk: "Профілі вузлів кластера для стендів: ресурси та місткість.",
		En: "Cluster node profiles for stands: resources and capacity.",
	},
}

// DescribeKey returns the description of a platform setting key, or false when it has none.
func DescribeKey(key string) (Description, bool) {
	d, ok := descriptions[key]
	return d, ok
}
