package startupdefaults

func init() {
	inAppLook["resources.change.requested"] = [2]string{"mail", "info"}
	inAppLook["resources.change.approved"] = [2]string{"success", "success"}
	inAppLook["resources.change.rejected"] = [2]string{"warning", "warning"}
	inAppLook["resources.alarm.raised"] = [2]string{"warning", "warning"}
}
