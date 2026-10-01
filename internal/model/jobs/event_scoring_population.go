package jobsModel

// EventScoringPopulationArgs drives the idempotent event-start reconcile.
type EventScoringPopulationArgs struct{}

func (EventScoringPopulationArgs) Kind() string { return "event_scoring_population" }
