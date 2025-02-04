package model

import "github.com/cybericebox/lib/pkg/err"

// Object codes
const (
	PlatformObjectCode = iota
	PostgresObjectCode
	AgentObjectCode
	VPNObjectCode
	StorageObjectCode
	EmailObjectCode
	TemporalCoreObjectCode
	AuthObjectCode
	AuthRecaptchaObjectCode
	UserObjectCode
	LaboratoryObjectCode
	ExerciseObjectCode
	ExerciseCategoryObjectCode
	EventObjectCode
	EventChallengeObjectCode
	EventChallengeCategoryObjectCode
	EventParticipantObjectCode
	EventScoreObjectCode
	EventTeamObjectCode
	EventTeamChallengeObjectCode
)

var (
	ErrPostgres = err.ErrInternal.WithObjectCode(PostgresObjectCode)
	ErrAgent    = err.ErrInternal.WithObjectCode(AgentObjectCode)
)
