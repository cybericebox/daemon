package lab

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cybericebox/daemon/internal/model/errorJournal"
)

// ThrottleJournalWindow is how often one device of one lab is written to the journal while it stays throttled.
const ThrottleJournalWindow = time.Hour

// ThrottleJournal returns a ThrottleNotifier that writes a throttled device to the error journal (visible to
// super admins only) instead of asking anyone to decide. The limits of a task device are set by the task author,
// so it is no alarm for an event organizer or an admin on duty. One entry per lab and device per window: the
// journal groups them by the device (the key has no lab run), so the group's counter is how often it happened.
// The entry records only; nobody is messaged.
func ThrottleJournal(report func(errorJournal.Event), now func() time.Time) ThrottleNotifier {
	var mu sync.Mutex
	last := map[string]time.Time{}
	return func(_ context.Context, a ThrottleAlert) error {
		key := "cpu_throttle:" + a.LabGroupName + "/" + a.LabName + "/" + a.Device
		at := now()
		mu.Lock()
		if t, seen := last[key]; seen && at.Sub(t) < ThrottleJournalWindow {
			mu.Unlock()
			return nil
		}
		for k, t := range last {
			if at.Sub(t) >= ThrottleJournalWindow {
				delete(last, k)
			}
		}
		last[key] = at
		mu.Unlock()
		report(errorJournal.Event{
			Kind:    errorJournal.KindLabComponent,
			Source:  "cpu_throttle",
			Message: fmt.Sprintf("Device %s of lab %s hits its CPU limit: %d%% of CPU periods throttled over %d s", a.Device, a.LabName, a.Percent, a.WindowSeconds),
			Key:     key,
			Notify:  errorJournal.NotifyNever,
			Details: map[string]string{"agent_id": a.AgentID, "lab_group": a.LabGroupName, "lab": a.LabName, "device": a.Device, "percent": fmt.Sprint(a.Percent)},
			At:      at,
		})
		return nil
	}
}
