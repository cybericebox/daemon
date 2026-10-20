package inappdefaults

import "testing"

// The resource requests open the admin resources page; admin_url is the admin site root the emitter provides.
func TestResourceRequestsLinkToTheAdminResourcesPage(t *testing.T) {
	for _, lang := range []Lang{UK, EN} {
		found := 0
		for _, tpl := range All(lang) {
			if tpl.Type != "resources.change.requested" && tpl.Type != "resources.alarm.raised" {
				continue
			}
			found++
			if tpl.Link != "{{.admin_url}}resources/" {
				t.Errorf("%s %s: link %q", lang, tpl.Type, tpl.Link)
			}
		}
		if found != 2 {
			t.Errorf("%s: %d resource request templates, want 2", lang, found)
		}
	}
}

// A CPU throttling request has no page of its own to open (the resources page is the reservation calendar), so it
// carries no link and the inbox offers only «Вирішено».
func TestCPUThrottlingRequestHasNoLink(t *testing.T) {
	for _, lang := range []Lang{UK, EN} {
		found := 0
		for _, tpl := range All(lang) {
			if tpl.Type != "resources.cpu.throttled" {
				continue
			}
			found++
			if tpl.Link != "" {
				t.Errorf("%s: link %q", lang, tpl.Link)
			}
		}
		if found != 1 {
			t.Errorf("%s: %d CPU throttling templates, want 1", lang, found)
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
