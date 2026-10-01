package labMonitoring

import "encoding/json"

// Launch is the launch-pacing and image state of one lab group (or of a whole stand) read
// from the current monitoring payload: how many of its labs wait in the operator's scheduler
// queue, where the best placed one stands and whether an image is pulled by tag.
type Launch struct {
	QueuedLabs int
	// Position and Length belong to the queued lab with the smallest position; Reason is why it waits.
	Position, Length int32
	Reason           string
	// ImageWarning is true when a lab or the group has an image that could not be pinned to a digest.
	ImageWarning bool
}

// Add folds another group's state into l.
func (l *Launch) Add(o Launch) {
	if o.QueuedLabs > 0 && (l.QueuedLabs == 0 || o.Position < l.Position) {
		l.Position, l.Length, l.Reason = o.Position, o.Length, o.Reason
	}
	l.QueuedLabs += o.QueuedLabs
	l.ImageWarning = l.ImageWarning || o.ImageWarning
}

// PayloadLaunch reads the launch queue and image warnings of one lab group's current
// monitoring state. A payload that cannot be read counts as nothing observed.
func PayloadLaunch(raw json.RawMessage) Launch {
	var payload struct {
		Groups []struct {
			Status struct {
				ImageWarning string `json:"imageWarning"`
			} `json:"status"`
		} `json:"groups"`
		Labs []struct {
			Status struct {
				Phase        string `json:"phase"`
				ImageWarning string `json:"imageWarning"`
				Scheduling   *struct {
					Position flexInt `json:"position"`
					Length   flexInt `json:"length"`
					Reason   string  `json:"reason"`
				} `json:"scheduling"`
			} `json:"status"`
		} `json:"labs"`
	}
	var out Launch
	if json.Unmarshal(raw, &payload) != nil {
		return out
	}
	for _, g := range payload.Groups {
		out.ImageWarning = out.ImageWarning || g.Status.ImageWarning != ""
	}
	for _, lab := range payload.Labs {
		out.ImageWarning = out.ImageWarning || lab.Status.ImageWarning != ""
		q := lab.Status.Scheduling
		if lab.Status.Phase != "Queued" || q == nil {
			continue
		}
		one := Launch{QueuedLabs: 1, Position: int32(q.Position), Length: int32(q.Length), Reason: q.Reason}
		out.Add(one)
	}
	return out
}
