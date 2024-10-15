package model

import "github.com/cybericebox/daemon/internal/appError"

// Object codes
const (
	unknownObjectCode = iota
	platformObjectCode
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
	ErrPlatform = appError.ErrInternal.WithObjectCode(platformObjectCode)
	ErrPostgres = appError.ErrInternal.WithObjectCode(postgresObjectCode)
	ErrAgent    = appError.ErrInternal.WithObjectCode(agentObjectCode)
	ErrVPN      = appError.ErrInternal.WithObjectCode(vpnObjectCode)
)
