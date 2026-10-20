package inappdefaults

import "testing"

// The resource requests open the admin resources page; admin_url is the admin site root the emitter provides.
func TestResourceRequestsLinkToTheAdminResourcesPage(t *testing.T) {
	for _, lang := range []Lang{UK, EN} {
		found := 0
		for _, tpl := range All(lang) {
			if tpl.Type != "resources.change.requested" && tpl.Type != "resources.alarm.raised" && tpl.Type != "resources.cpu.throttled" {
				continue
			}
			found++
			if tpl.Link != "{{.admin_url}}resources/" {
				t.Errorf("%s %s: link %q", lang, tpl.Type, tpl.Link)
			}
		}
		if found != 3 {
			t.Errorf("%s: %d resource request templates, want 3", lang, found)
		}
	}
}

// A granted role opens the event's staff pages; the other grant notices have no staff page to open.
func TestManagerAssignedLinksToTheStaffPages(t *testing.T) {
	for _, lang := range []Lang{UK, EN} {
		for _, tpl := range All(lang) {
			if tpl.Type == "event.manager.assigned" && tpl.Link != "{{.event_url}}manage/" {
				t.Errorf("%s: link %q", lang, tpl.Link)
			}
		}
	}
}
