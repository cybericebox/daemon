package event

import eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"

func plannedUsers(configured, actual int, known, frozen bool) int {
	if frozen && known {
		return max(actual, 1)
	}
	if configured > 0 {
		return configured
	}
	return int(eventConfigModel.DefaultMaxTeamSize())
}
