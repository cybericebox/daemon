package lab

import (
	"context"
	"sync"

	"github.com/rs/zerolog/log"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// ThrottleAlertPercent is the share of throttled CFS periods above which a device is starved by its CPU limit.
const ThrottleAlertPercent = 50

// labKind is the deleted-key kind of a lab.
const labKind = "Lab"

// ThrottleAlert is one device that the agent reports throttled above the threshold.
// Run identifies the lab run (the lab uid, or namespace/name without one).
type ThrottleAlert struct {
	AgentID, Namespace, LabName, LabGroupName, Run, Device string
	Percent, WindowSeconds                                 int
}

// ThrottleNotifier delivers an alert (the staff inbox). A failure is retried with the next report.
type ThrottleNotifier func(ctx context.Context, alert ThrottleAlert) error

// ThrottleGone closes the alert of a device whose lab run is gone (the staff inbox). A failure is logged only:
// an admin may still close the request by hand.
type ThrottleGone func(ctx context.Context, run, device string) error

// ThrottleWatcher raises one alert per device per lab run. A lab run is the lab object (its uid); a lab that is
// deleted, or missing from a snapshot, starts a new run when it comes back. What was alerted lives in memory: a
// restarted daemon may repeat an alert for a device that is still throttled.
type ThrottleWatcher struct {
	agentID string
	notify  ThrottleNotifier
	gone    ThrottleGone
	mu      sync.Mutex
	// alerted is the devices already alerted, by lab run key.
	alerted map[string]map[string]bool
	// runs maps namespace/name of a lab to its run key, for the deleted keys that carry no uid.
	runs map[string]string
}

func NewThrottleWatcher(agentID string, notify ThrottleNotifier) *ThrottleWatcher {
	return &ThrottleWatcher{agentID: agentID, notify: notify, alerted: map[string]map[string]bool{}, runs: map[string]string{}}
}

// WithGone sets what closes the alerts of a lab run that disappeared; without it they stay open.
func (w *ThrottleWatcher) WithGone(gone ThrottleGone) *ThrottleWatcher {
	w.gone = gone
	return w
}

// goneAlert is one alerted device of a lab run that disappeared.
type goneAlert struct{ run, device string }

// Observe looks at the labs of one monitoring message. It never blocks the stream on a failing notifier.
func (w *ThrottleWatcher) Observe(ctx context.Context, update *labpb.MonitoringUpdate) {
	if w == nil || w.notify == nil {
		return
	}
	for _, g := range w.observe(ctx, update) {
		if err := w.gone(ctx, g.run, g.device); err != nil {
			log.Error().Err(err).Str("run", g.run).Str("device", g.device).Msg("Failed to close the CPU throttling alert of a gone lab")
		}
	}
}

// observe updates the state under the lock and returns the alerts of the lab runs that disappeared (only when
// something closes them).
func (w *ThrottleWatcher) observe(ctx context.Context, update *labpb.MonitoringUpdate) []goneAlert {
	w.mu.Lock()
	defer w.mu.Unlock()
	var gone []goneAlert
	forget := func(run string) {
		if w.gone != nil {
			for device := range w.alerted[run] {
				gone = append(gone, goneAlert{run: run, device: device})
			}
		}
		delete(w.alerted, run)
	}
	if update.GetSnapshot() {
		present := map[string]bool{}
		for _, l := range update.GetLabs() {
			present[runKey(l)] = true
		}
		for run := range w.alerted {
			if !present[run] {
				forget(run)
			}
		}
		for name, run := range w.runs {
			if !present[run] {
				delete(w.runs, name)
			}
		}
	}
	for _, deleted := range update.GetDeletedKeys() {
		if deleted.GetKind() != labKind {
			continue
		}
		name := deleted.GetNamespace() + "/" + deleted.GetName()
		if run, ok := w.runs[name]; ok {
			forget(run)
		}
		delete(w.runs, name)
	}
	for _, l := range update.GetLabs() {
		run := runKey(l)
		w.runs[l.GetNamespace()+"/"+l.GetName()] = run
		// Test deploys (the catalog, an event preview) run with a deliberately small CPU: only the labs of an
		// event team are alerted.
		if _, _, ok := labBindingModel.ParseGroupName(l.GetLabGroupName()); !ok {
			continue
		}
		for _, d := range l.GetStatus().GetDevices() {
			if d.GetCpuThrottledPercent() <= ThrottleAlertPercent || w.alerted[run][d.GetName()] {
				continue
			}
			alert := ThrottleAlert{
				AgentID: w.agentID, Namespace: l.GetNamespace(), LabName: l.GetName(), LabGroupName: l.GetLabGroupName(), Run: run,
				Device: d.GetName(), Percent: int(d.GetCpuThrottledPercent()), WindowSeconds: int(d.GetCpuThrottleWindowSeconds()),
			}
			if err := w.notify(ctx, alert); err != nil {
				log.Error().Err(err).Str("lab", l.GetName()).Str("device", d.GetName()).Msg("Failed to raise the CPU throttling alert")
				continue
			}
			if w.alerted[run] == nil {
				w.alerted[run] = map[string]bool{}
			}
			w.alerted[run][d.GetName()] = true
		}
	}
	return gone
}

func runKey(l *labpb.Lab) string {
	if l.GetUid() != "" {
		return l.GetUid()
	}
	return l.GetNamespace() + "/" + l.GetName()
}
