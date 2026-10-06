package jobsModel

// EventAnalyticsArgs is the empty payload of the event analytics rollup pass.
type EventAnalyticsArgs struct{}

func (EventAnalyticsArgs) Kind() string { return "event_analytics" }
