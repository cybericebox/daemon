package tools

import (
	"errors"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"regexp"
)

const (
	defaultContextKey = "detailed"
)

var (
	keyValueRegex = regexp.MustCompile(`Key \(([^)]+)\)=\(([^)]+)\)`)

	fieldFunctions = []struct {
		fieldName      string
		tableName      string
		isDeleteAction bool
		errorFunc      err.ErrorCreator
	}{
		{"updated_by", "", false, userModel.ErrUserUserNotFound},
		{"user_id", "", false, userModel.ErrUserUserNotFound},
		{"event_id", "", false, eventModel.ErrEventEventNotFound},
		{"category_id", "event_challenges", true, eventModel.ErrEventChallengeCategoryCategoryHasChallenges},
		{"category_id", "event_challenges", false, eventModel.ErrEventChallengeCategoryCategoryNotFound},
		{"category_id", "exercise_categories", true, exerciseModel.ErrExerciseCategoryCategoryHasExercises},
		{"category_id", "exercise_categories", false, exerciseModel.ErrExerciseCategoryCategoryNotFound},
		{"challenge_id", "", false, eventModel.ErrEventChallengeChallengeNotFound},
		{"team_id", "", false, eventModel.ErrEventTeamTeamNotFound},
		{"exercise_id", "", true, exerciseModel.ErrExerciseExerciseInUse},
		{"exercise_id", "", false, exerciseModel.ErrExerciseExerciseNotFound},
	}
)

func IsObjectNotFoundError(err error) bool {
	if err != nil {
		return errors.Is(err, pgx.ErrNoRows)
	}
	return false
}

func UniqueViolationError(err error, creator err.ErrorCreator) (err.ErrorCreator, bool) {
	if err != nil {
		var perr *pgconn.PgError
		if errors.As(err, &perr) {
			if perr.Code == pgerrcode.UniqueViolation {
				if matches := keyValueRegex.FindStringSubmatch(perr.Detail); len(matches) == 3 {
					contextKey, contextValue := matches[1], matches[2]
					return creator.WithContext(contextKey, contextValue), true
				}

				return creator.WithContext(defaultContextKey, err.Error()), true
			}
		}
	}
	return nil, false
}

func ForeignKeyViolationError(err error, isDelete ...bool) (err.ErrorCreator, bool) {
	isDeleteAction := false
	if len(isDelete) > 0 {
		isDeleteAction = isDelete[0]
	}
	if err != nil {
		var perr *pgconn.PgError
		if errors.As(err, &perr) {
			if perr.Code == pgerrcode.ForeignKeyViolation {
				if matches := keyValueRegex.FindStringSubmatch(perr.Detail); len(matches) == 3 {
					contextKey, contextValue := matches[1], matches[2]
					for _, fieldFunc := range fieldFunctions {
						if fieldFunc.fieldName == contextKey {
							if fieldFunc.tableName == "" || fieldFunc.tableName == perr.TableName {
								if fieldFunc.isDeleteAction == isDeleteAction {
									return fieldFunc.errorFunc.WithContext(contextKey, contextValue), true
								}
							}
						}
					}

					return model.ErrPlatform.WithMessage(perr.Message).WithContext(contextKey, contextValue), true
				}
			}
		}
	}
	return nil, false
}

//IntegrityConstraintViolation, RestrictViolation, NotNullViolation, ForeignKeyViolationError, UniqueViolation, CheckViolation, ExclusionViolation
