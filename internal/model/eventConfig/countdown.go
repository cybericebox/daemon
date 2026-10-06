package eventConfigModel

const (
	defaultFinishCountdownMinutes int32 = 10
	maxFinishCountdownMinutes     int32 = 1440
)

// FinishCountdownMode says when the time-left countdown is visible.
type FinishCountdownMode string

const (
	// FinishBeforeEnd is today's behaviour: visible only during the last FinishMinutes.
	FinishBeforeEnd FinishCountdownMode = "before_end"
	// FinishFromStart shows it from the beginning of the current stage (of the event when it has no stages).
	FinishFromStart FinishCountdownMode = "from_start"
)

// Valid reports whether the mode is one of the two.
func (m FinishCountdownMode) Valid() bool { return m == FinishBeforeEnd || m == FinishFromStart }

// CountdownSettings says which countdowns the participant «Завдання» and
// «Результати» pages show: to the start, and to the effective finish during
// the last FinishMinutes only (or from the start of the current stage, per FinishMode). One setting per event,
// applied to the end of every stage.
type CountdownSettings struct {
	ShowStart     bool
	ShowFinish    bool
	FinishMinutes int32
	FinishMode    FinishCountdownMode
}

func DefaultCountdownSettings() CountdownSettings {
	return CountdownSettings{ShowStart: true, ShowFinish: true, FinishMinutes: defaultFinishCountdownMinutes, FinishMode: FinishBeforeEnd}
}

func (s CountdownSettings) valid() bool {
	return s.FinishMinutes >= 1 && s.FinishMinutes <= maxFinishCountdownMinutes && (s.FinishMode == "" || s.FinishMode.Valid())
}

// Mode is the finish mode with the default for an unset value.
func (s CountdownSettings) Mode() FinishCountdownMode {
	if s.FinishMode == "" {
		return FinishBeforeEnd
	}
	return s.FinishMode
}
