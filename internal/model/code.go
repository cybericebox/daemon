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
	IPAMObjectCode
	WgKeyGenObjectCode
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
)
