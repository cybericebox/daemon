package settingsUseCase

import (
	"github.com/cybericebox/daemon/internal/delivery/repository/notificationSettingsRepo"
)

type (
	Dependencies struct {
		Repo notificationSettingsRepo.Queries
	}
	NotificationSettingsUseCase struct {
		settings *notificationSettingsRepo.Repository
	}
)

func NewNotificationSettingsUseCase(deps Dependencies) *NotificationSettingsUseCase {
	return &NotificationSettingsUseCase{settings: notificationSettingsRepo.New(deps.Repo)}
}
