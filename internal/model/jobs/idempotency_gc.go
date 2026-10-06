package jobsModel

// IdempotencyGCArgs is the empty payload of the periodic replay-record GC.
type IdempotencyGCArgs struct{}

func (IdempotencyGCArgs) Kind() string { return "idempotency_gc" }
