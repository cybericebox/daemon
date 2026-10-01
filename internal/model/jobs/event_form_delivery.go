package jobsModel

// EventFormDeliveryArgs drives periodic materialization of timed event forms.
type EventFormDeliveryArgs struct{}

func (EventFormDeliveryArgs) Kind() string { return "event_form_delivery" }
