package jobsModel

// EventStandsArgs drives the schedule-driven team stand engine: preparing
// every team's challenges, deploying and observing stand Labs, the strict
// availability barrier and the final teardown.
type EventStandsArgs struct{}

func (EventStandsArgs) Kind() string { return "event_stands" }
