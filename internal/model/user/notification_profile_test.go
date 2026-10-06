package userModel

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestUserNotificationProfileExcludesSensitiveFields(t *testing.T) {
	u := User{
		ID:             uuid.Must(uuid.NewV4()),
		Email:          "participant@example.org",
		FirstName:      "Ada",
		LastName:       "Lovelace",
		Picture:        "avatars/ada.png",
		HashedPassword: "must-not-cross-notification-boundary",
	}

	profile := u.NotificationProfile()
	require.Equal(t, u.ID, profile.ID)
	require.Equal(t, u.Email, profile.Email)
	require.Equal(t, u.FirstName, profile.FirstName)
	require.Equal(t, u.LastName, profile.LastName)
	require.Equal(t, u.Picture, profile.Picture)
}
