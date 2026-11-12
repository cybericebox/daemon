package model

// Object codes — one per domain, used to namespace error detail codes.
const (
	PlatformObjectCode = iota
	SettingObjectCode
	NotificationObjectCode
	UserObjectCode
	AuthObjectCode
	TemporalCodeObjectCode
	AuthRecaptchaObjectCode
	// Infrastructure packages (pkg/...) also namespace their error codes here
	// so the registry stays single and collision-free.
	IPAMObjectCode     // reserved: it was pkg/ipam (removed); the constant stays so the object codes after it keep their numbers
	WgKeyGenObjectCode // reserved: it was pkg/wgkeygen (removed); kept so the codes after it keep their numbers
	ExerciseObjectCode
	MediaObjectCode
	EventObjectCode
	EventConfigObjectCode
	ParticipantObjectCode
	InfrastructureObjectCode
	VPNConfigObjectCode
	EventManagerObjectCode
	EventTeamObjectCode
	EventExerciseObjectCode
	EventChallengeObjectCode
	EventStandObjectCode
	MailObjectCode
	EventAnalyticsObjectCode
	PlatformAnalyticsObjectCode
	ErrorJournalObjectCode
	ResourceCalendarObjectCode
	EventLabObjectCode
	ParticipantPreviewObjectCode
	ImageRegistryObjectCode
	ExerciseImportObjectCode
	LinkedVariantObjectCode
	NodeProfileObjectCode
	DeviceObjectCode
	PasskeyObjectCode
	TablePrefsObjectCode
	ExerciseDocumentObjectCode
)
