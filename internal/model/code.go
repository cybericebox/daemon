package model

import "github.com/cybericebox/lib/pkg/err"

// Object codes
const (
	platformObjectCode = iota
	postgresObjectCode
	agentObjectCode
	vpnObjectCode
	storageObjectCode
	emailObjectCode
	temporalCoreObjectCode
	authObjectCode
	authRecaptchaObjectCode
	userObjectCode
	laboratoryObjectCode
	exerciseObjectCode
	exerciseCategoryObjectCode
	eventObjectCode
	eventChallengeObjectCode
	eventChallengeCategoryObjectCode
	eventParticipantObjectCode
	eventScoreObjectCode
	eventTeamObjectCode
	eventTeamChallengeObjectCode
)

var (
	ErrPlatform = err.ErrInternal.WithObjectCode(platformObjectCode)
	ErrPostgres = err.ErrInternal.WithObjectCode(postgresObjectCode)
	ErrAgent    = err.ErrInternal.WithObjectCode(agentObjectCode)
	ErrVPN      = err.ErrInternal.WithObjectCode(vpnObjectCode)
)
