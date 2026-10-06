package emaildefaults

// StartupSeeded are the template types created at daemon start from these defaults instead of by
// a migration (see app.seedNotificationDefaults). Database tests that compare the migrated rows
// with the defaults skip them.
var StartupSeeded = map[string]bool{"email_changed": true}

// All returns the default templates of lang, one per notification type.
func All(lang Lang) []Template {
	if lang == EN {
		return enTemplates()
	}
	return ukTemplates()
}
