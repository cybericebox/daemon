package emaildefaults

// All returns the default templates of lang, one per notification type.
func All(lang Lang) []Template {
	if lang == EN {
		return enTemplates()
	}
	return ukTemplates()
}
