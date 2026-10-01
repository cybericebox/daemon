package repositoryTools

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
)

// ---------------------------------------------------------------------------
// AnyToString
// ---------------------------------------------------------------------------

func TestAnyToString_String(t *testing.T) {
	assert.Equal(t, "hello", AnyToString("hello"))
}

func TestAnyToString_Bytes(t *testing.T) {
	assert.Equal(t, "world", AnyToString([]byte("world")))
}

func TestAnyToString_Nil(t *testing.T) {
	assert.Equal(t, "", AnyToString(nil))
}

func TestAnyToString_Int(t *testing.T) {
	assert.Equal(t, "", AnyToString(42))
}

// ---------------------------------------------------------------------------
// IsObjectNotFoundError
// ---------------------------------------------------------------------------

func TestIsObjectNotFoundError_ErrNoRows(t *testing.T) {
	assert.True(t, IsObjectNotFoundError(pgx.ErrNoRows))
}

func TestIsObjectNotFoundError_Wrapped(t *testing.T) {
	wrapped := fmt.Errorf("repo: %w", pgx.ErrNoRows)
	assert.True(t, IsObjectNotFoundError(wrapped))
}

func TestIsObjectNotFoundError_Nil(t *testing.T) {
	assert.False(t, IsObjectNotFoundError(nil))
}

func TestIsObjectNotFoundError_OtherError(t *testing.T) {
	assert.False(t, IsObjectNotFoundError(errors.New("some other error")))
}

// ---------------------------------------------------------------------------
// UniqueViolationError
// ---------------------------------------------------------------------------

func TestUniqueViolationError_WithKeyValue(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:   pgerrcode.UniqueViolation,
		Detail: "Key (email)=(a@b.c) already exists.",
	}
	creator := notificationModel.ErrTemplateNotFound

	result, ok := UniqueViolationError(pgErr, creator)
	require.True(t, ok, "expected unique violation to be detected")
	require.NotNil(t, result, "expected non-nil creator")

	// The returned ErrorCreator should carry the extracted context; verify it
	// produces an error string containing the key (uppercased by lib) and value.
	errStr := result.Err().Error()
	assert.Contains(t, errStr, "EMAIL")
	assert.Contains(t, errStr, "a@b.c")
}

func TestUniqueViolationError_NoKeyValueInDetail(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:   pgerrcode.UniqueViolation,
		Detail: "duplicate key value violates unique constraint",
	}
	creator := notificationModel.ErrTemplateNotFound

	result, ok := UniqueViolationError(pgErr, creator)
	require.True(t, ok)
	require.NotNil(t, result)
	// Falls back to defaultContextKey + error string; key is uppercased by lib.
	errStr := result.Err().Error()
	assert.Contains(t, errStr, "DETAILED")
}

func TestUniqueViolationError_NonPgError(t *testing.T) {
	creator := notificationModel.ErrTemplateNotFound
	result, ok := UniqueViolationError(errors.New("not a pg error"), creator)
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestUniqueViolationError_Nil(t *testing.T) {
	creator := notificationModel.ErrTemplateNotFound
	result, ok := UniqueViolationError(nil, creator)
	assert.False(t, ok)
	assert.Nil(t, result)
}

// ---------------------------------------------------------------------------
// ForeignKeyViolationError
// ---------------------------------------------------------------------------

func TestForeignKeyViolationError_MappedToDispatchNotFound(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:      pgerrcode.ForeignKeyViolation,
		Message:   "insert or update on table violates foreign key constraint",
		TableName: "notification_dispatch_targets",
		Detail:    "Key (dispatch_id)=(abc-123) is not present in table \"notification_dispatches\".",
	}

	result, ok := ForeignKeyViolationError(pgErr)
	require.True(t, ok, "expected FK violation to be detected")
	require.NotNil(t, result)

	// The fieldFunctions mapping routes dispatch_id on this table to
	// ErrDispatchNotFound; verify via the lib's status-code-based .Is(...).
	assert.True(
		t, result.Err().Is(notificationModel.ErrDispatchNotFound.Err()),
		"expected returned error to match ErrDispatchNotFound",
	)

	// Key is uppercased by the lib's error formatter; value preserved.
	errStr := result.Err().Error()
	assert.Contains(t, errStr, "DISPATCH_ID")
	assert.Contains(t, errStr, "abc-123")
}

func TestForeignKeyViolationError_UnmappedKeyFallsBackToPlatform(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:    pgerrcode.ForeignKeyViolation,
		Message: "insert or update on table violates foreign key constraint",
		Detail:  "Key (other_id)=(x) is not present in table \"others\".",
	}

	result, ok := ForeignKeyViolationError(pgErr)
	require.True(t, ok)
	require.NotNil(t, result)

	// Not in fieldFunctions -> ErrPlatform-based fallback, NOT ErrDispatchNotFound.
	assert.False(
		t, result.Err().Is(notificationModel.ErrDispatchNotFound.Err()),
		"unmapped key must not resolve to ErrDispatchNotFound",
	)
	errStr := result.Err().Error()
	assert.Contains(t, errStr, "OTHER_ID")
}

func TestForeignKeyViolationError_NoKeyValueInDetail(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:    pgerrcode.ForeignKeyViolation,
		Message: "foreign key violation",
		Detail:  "something without key-value pattern",
	}

	// No regex match -> the inner block returns nothing, so overall (nil, false).
	result, ok := ForeignKeyViolationError(pgErr)
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestForeignKeyViolationError_UniqueViolationPgError(t *testing.T) {
	pgErr := &pgconn.PgError{
		Code:   pgerrcode.UniqueViolation,
		Detail: "Key (email)=(a@b.c) already exists.",
	}

	result, ok := ForeignKeyViolationError(pgErr)
	assert.False(t, ok)
	assert.Nil(t, result)
}

func TestForeignKeyViolationError_Nil(t *testing.T) {
	result, ok := ForeignKeyViolationError(nil)
	assert.False(t, ok)
	assert.Nil(t, result)
}

// ---------------------------------------------------------------------------
// IsForeignKeyViolation
// ---------------------------------------------------------------------------

func TestIsForeignKeyViolation(t *testing.T) {
	assert.True(t, IsForeignKeyViolation(fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: pgerrcode.ForeignKeyViolation})))
	assert.False(t, IsForeignKeyViolation(&pgconn.PgError{Code: pgerrcode.UniqueViolation}))
	assert.False(t, IsForeignKeyViolation(errors.New("plain")))
	assert.False(t, IsForeignKeyViolation(nil))
}
