package rbac

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidRole(t *testing.T) {
	assert.True(t, ValidRole("super_admin"))
	assert.True(t, ValidRole("admin"))
	assert.True(t, ValidRole("admin_viewer"))
	assert.True(t, ValidRole("user"))
	// public is the unauthenticated sentinel; it is never stored.
	assert.False(t, ValidRole("public"))
	assert.False(t, ValidRole("bogus"))
}
