package repositoryTools

import (
	"errors"
	"regexp"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	liberr "github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
)

const (
	defaultContextKey = "detailed"
)

var (
	keyValueRegex = regexp.MustCompile(`Key \(([^)]+)\)=\(([^)]+)\)`)

	// foreignKeyFieldFunctions maps a foreign-key column (optionally scoped to a table and
	// to whether the failing statement was a DELETE) to the domain error returned
	// for that violation. Mirrors the daemon's table; cleaned to AP's current
	// schema. Add entries here as AP's domains land.
	foreignKeyFieldFunctions = []struct {
		fieldName      string
		tableName      string
		isDeleteAction bool
		errorFunc      liberr.ErrorCreator
	}{
		{
			"dispatch_id",
			"notification_dispatch_targets",
			false,
			notificationModel.ErrDispatchNotFound,
		},
	}
)

// AnyToString converts a value scanned into an interface{} column (e.g., a SQL
// string concatenation such as first_name || ' ' || last_name, which sqlc types
// as interface{}) into a string. Non-string and nil values yield "".
func AnyToString(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return ""
	}
}

func IsObjectNotFoundError(err error) bool {
	if err != nil {
		return errors.Is(err, pgx.ErrNoRows)
	}
	return false
}

func UniqueViolationError(err error, creator liberr.ErrorCreator) (liberr.ErrorCreator, bool) {
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			if pgErr.Code == pgerrcode.UniqueViolation {
				if matches := keyValueRegex.FindStringSubmatch(pgErr.Detail); len(matches) == 3 {
					contextKey, contextValue := matches[1], matches[2]
					return creator.WithContext(contextKey, contextValue), true
				}

				return creator.WithContext(defaultContextKey, err.Error()), true
			}
		}
	}
	return nil, false
}

// ForeignKeyViolationError returns a domain error for a foreign-key violation, if the error is a foreign-key violation.
// The optional isDelete argument indicates whether the failing statement was a DELETE (true) or an INSERT/UPDATE (false).
// If the error is not a foreign-key violation, it returns nil and false.
func ForeignKeyViolationError(err error, isDelete ...bool) (liberr.ErrorCreator, bool) {
	isDeleteAction := false
	if len(isDelete) > 0 {
		isDeleteAction = isDelete[0]
	}
	if err != nil {
		if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
			if pgErr.Code == pgerrcode.ForeignKeyViolation {
				if matches := keyValueRegex.FindStringSubmatch(pgErr.Detail); len(matches) == 3 {
					contextKey, contextValue := matches[1], matches[2]
					for _, fieldFunc := range foreignKeyFieldFunctions {
						if fieldFunc.fieldName == contextKey {
							if fieldFunc.tableName == "" || fieldFunc.tableName == pgErr.TableName {
								if fieldFunc.isDeleteAction == isDeleteAction {
									return fieldFunc.errorFunc.WithContext(
										contextKey,
										contextValue,
									), true
								}
							}
						}
					}

					return model.ErrPlatform.WithMessage(pgErr.Message).
							WithContext(contextKey, contextValue),
						true
				}
			}
		}
	}
	return nil, false
}

// IsForeignKeyViolation reports whether err is a Postgres foreign-key
// violation (23503), e.g. a DELETE blocked by an ON DELETE RESTRICT reference.
func IsForeignKeyViolation(err error) bool {
	pgErr, ok := errors.AsType[*pgconn.PgError](err)
	return ok && pgErr.Code == pgerrcode.ForeignKeyViolation
}

// IntegrityConstraintViolation, RestrictViolation, NotNullViolation, ForeignKeyViolationError, UniqueViolation, CheckViolation, ExclusionViolation
