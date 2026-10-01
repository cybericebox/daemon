package jobsModel

// ResultChangeGCArgs is the empty payload of the short-lived live-result GC.
type ResultChangeGCArgs struct{}

func (ResultChangeGCArgs) Kind() string { return "result_change_gc" }
