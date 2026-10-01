package jobsModel

// EventMailArgs drives the Event mail notices: start reminders, finish
// notices and expired invitations (each announced once).
type EventMailArgs struct{}

func (EventMailArgs) Kind() string { return "event_mail" }
