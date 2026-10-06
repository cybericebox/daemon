package jobsModel

// ResourceCalendarArgs drives the readiness check of the resource calendar: it completes the teams that had no
// agent when capacity appears (it only adds, nothing placed is moved), raises, escalates and resolves the
// readiness alarms, and drops the expired test laboratory holds.
type ResourceCalendarArgs struct{}

func (ResourceCalendarArgs) Kind() string { return "resource_calendar_check" }
