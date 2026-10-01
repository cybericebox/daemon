package jobsModel

// SignalProcessingArgs starts one bounded pass over due durable signals.
type SignalProcessingArgs struct{}

func (SignalProcessingArgs) Kind() string { return "signal_processing" }
