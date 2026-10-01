package eventConfigModel

const (
	defaultFinishCountdownMinutes int32 = 10
	maxFinishCountdownMinutes     int32 = 1440
)

// CountdownSettings says which countdowns the participant «Завдання» and
// «Результати» pages show: to the start, and to the effective finish during
// the last FinishMinutes only.
type CountdownSettings struct {
	ShowStart     bool
	ShowFinish    bool
	FinishMinutes int32
}

func DefaultCountdownSettings() CountdownSettings {
	return CountdownSettings{ShowStart: true, ShowFinish: true, FinishMinutes: defaultFinishCountdownMinutes}
}

func (s CountdownSettings) valid() bool {
	return s.FinishMinutes >= 1 && s.FinishMinutes <= maxFinishCountdownMinutes
}
