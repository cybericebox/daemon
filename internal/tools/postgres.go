package tools

import (
	"errors"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
)

func IsObjectNotFoundError(err error) bool {
	if err != nil {

		return errors.Is(err, pgx.ErrNoRows)
	}
	return false
}

func IsUniqueViolationError(err error) bool {
	if err != nil {
		var perr *pgconn.PgError
		if errors.As(err, &perr) {
			return perr.Code == pgerrcode.UniqueViolation
		}
	}
	return false
}

// TODO: foreign key violation error can be caused by multiple reasons: the foreign key does not exist on create or update, foreign key value does exists on delete
func ForeignKeyViolationError(err error, isDelete ...bool) (appError.ErrorCreator, bool) {
	isDeleteAction := false
	if len(isDelete) > 0 {
		isDeleteAction = isDelete[0]
	}
	if err != nil {
		var perr *pgconn.PgError
		if errors.As(err, &perr) {
			if perr.Code == pgerrcode.ForeignKeyViolation {
				foreignKey, _ := strings.CutPrefix(perr.ConstraintName, perr.TableName+"_")
				foreignKey, _ = strings.CutSuffix(foreignKey, "_fkey")
				contextKey := strings.Replace(foreignKey, "_", "", -1)
				contextValue := strings.Split(strings.Split(perr.Detail, "(")[2], ")")[0]
				switch foreignKey {
				// userID
				case "updated_by", "user_id":
					return model.ErrUserUserNotFound.WithContext(contextKey, contextValue), true
				// eventID
				case "event_id":
					return model.ErrEventEventNotFound.WithContext(contextKey, contextValue), true
				// challengeCategoryID or exerciseCategoryID
				case "category_id":
					if perr.TableName == "event_challenges" {
						if isDeleteAction {
							return model.ErrEventChallengeCategoryCategoryHasChallenges.WithContext(contextKey, contextValue), true
						}
						return model.ErrEventChallengeCategoryCategoryNotFound.WithContext(contextKey, contextValue), true
					} else {
						if isDeleteAction {
							return model.ErrExerciseCategoryCategoryHasExercises.WithContext(contextKey, contextValue), true
						}
						return model.ErrExerciseCategoryCategoryNotFound.WithContext(contextKey, contextValue), true
					}
				// challengeID
				case "challenge_id":
					return model.ErrEventChallengeChallengeNotFound.WithContext(contextKey, contextValue), true
				// teamID
				case "team_id":
					return model.ErrEventTeamTeamNotFound.WithContext(contextKey, contextValue), true
				// exerciseID
				case "exercise_id":
					if isDeleteAction {
						return model.ErrExerciseExerciseInUse.WithContext(contextKey, contextValue), true
					}
					return model.ErrExerciseExerciseNotFound.WithContext(contextKey, contextValue), true
				}
				return model.ErrPlatform.WithMessage(perr.Message).WithContext(contextKey, contextValue), true
			}
		}
	}
	return nil, false
}

//IntegrityConstraintViolation, RestrictViolation, NotNullViolation, ForeignKeyViolationError, UniqueViolation, CheckViolation, ExclusionViolation
