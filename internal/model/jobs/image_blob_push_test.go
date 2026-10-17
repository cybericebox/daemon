package jobsModel

import "testing"

// A blob push is retried a few times at most (only a failure to record its state is returned to River).
func TestImageBlobPushArgsInsertOpts(t *testing.T) {
	if got := (ImageBlobPushArgs{}).InsertOpts().MaxAttempts; got != 3 {
		t.Fatalf("max attempts %d", got)
	}
}
