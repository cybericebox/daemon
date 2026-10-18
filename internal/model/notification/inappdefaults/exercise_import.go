package inappdefaults

// The end of a background task import. Created at daemon start (see startupdefaults.Seed), like the registry check.
func init() {
	StartupSeeded["exercise.import.done"] = true
	StartupSeeded["exercise.import.failed"] = true
	uk = append(uk,
		Template{"exercise.import.done", "Імпорт завершено", "Імпортовано завдань: {{.count}}. Вони вже в каталозі.", ""},
		Template{"exercise.import.failed", "Імпорт не вдався", "Завдання не імпортовано, нічого не створено. Відкрийте каталог завдань і натисніть «Подробиці» біля імпорту.", ""},
	)
	en = append(en,
		Template{"exercise.import.done", "Import finished", "Tasks imported: {{.count}}. They are in the catalog now.", ""},
		Template{"exercise.import.failed", "Import failed", "The tasks were not imported, nothing was created. Open the task catalog and press «Details» next to the import.", ""},
	)
}
