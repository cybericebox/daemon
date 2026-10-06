package jobsModel

// MediaGCArgs is the (empty) payload of the periodic media GC job.
type MediaGCArgs struct{}

func (MediaGCArgs) Kind() string { return "media_gc" }
