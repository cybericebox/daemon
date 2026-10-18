package inboxModel

// A background task import ended: a note for the user who started it. They exist only as inbox messages.
const (
	TypeExerciseImportDone   = "exercise.import.done"
	TypeExerciseImportFailed = "exercise.import.failed"
)

func init() {
	categoryRules[TypeExerciseImportDone] = map[RecipientRole]Category{"": CategoryPersonal}
	categoryRules[TypeExerciseImportFailed] = map[RecipientRole]Category{"": CategoryPersonal}
}
