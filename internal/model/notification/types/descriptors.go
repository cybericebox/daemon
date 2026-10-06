package notificationTypes

import (
	"reflect"
)

// VariableDescriptor documents one template variable for the admin editor.
// Default is a STATIC preview placeholder (the `default` tag); it cannot embed
// runtime config and is not used by the dispatch render path.
type VariableDescriptor struct {
	Name        string
	Description string
	Default     string
}

// Descriptors return the cached variable descriptors for a type (no reflection
// on call). Drives the admin editor. Unknown type -> nil.
func Descriptors(t NotificationType) []VariableDescriptor {
	return descriptorsByType[t]
}

func reflectDescriptors(n NotificationPayload) []VariableDescriptor {
	t := reflect.Indirect(reflect.ValueOf(n)).Type()
	ds := make([]VariableDescriptor, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, ok := f.Tag.Lookup("var")
		if !ok {
			continue
		}
		ds = append(
			ds, VariableDescriptor{
				Name:        name,
				Description: f.Tag.Get("desc"),
				Default:     f.Tag.Get("default"),
			},
		)
	}
	return ds
}
