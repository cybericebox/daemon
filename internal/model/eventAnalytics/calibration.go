package eventAnalyticsModel

// MinCalibrationTeams is how many teams must have tried a task before its
// solve rate says anything about its difficulty.
const MinCalibrationTeams = 3

// Calibration verdicts (§6.3).
const (
	CalibrationOK           = "ok"
	CalibrationTooEasy      = "too_easy"
	CalibrationTooHard      = "too_hard"
	CalibrationInsufficient = "insufficient"
	// CalibrationUnknown: the task declares no known difficulty.
	CalibrationUnknown = "unknown"
)

// SolveBand is the solve rate (solvers ÷ teams that tried) a declared
// difficulty is expected to produce.
type SolveBand struct {
	Min float64
	Max float64
}

var solveBands = map[string]SolveBand{
	"elementary": {Min: 0.90, Max: 1.00},
	"trivial":    {Min: 0.80, Max: 1.00},
	"easy":       {Min: 0.55, Max: 0.90},
	"medium":     {Min: 0.30, Max: 0.70},
	"hard":       {Min: 0.10, Max: 0.45},
	"insane":     {Min: 0.00, Max: 0.20},
}

// Calibration is the declared difficulty measured against the real solve
// rate of a task.
type Calibration struct {
	Verdict  string
	Expected SolveBand
}

// SolveRate is solved ÷ tried, 0 while nobody tried.
func SolveRate(tried, solved int64) float64 {
	if tried <= 0 {
		return 0
	}
	return float64(solved) / float64(tried)
}

// Calibrate compares a task's real solve rate with the band of its declared
// difficulty. A task tried by fewer than MinCalibrationTeams teams is not
// judged.
func Calibrate(difficulty string, tried, solved int64) Calibration {
	band, ok := solveBands[difficulty]
	if !ok {
		return Calibration{Verdict: CalibrationUnknown}
	}
	c := Calibration{Verdict: CalibrationOK, Expected: band}
	rate := SolveRate(tried, solved)
	switch {
	case tried < MinCalibrationTeams:
		c.Verdict = CalibrationInsufficient
	case rate > band.Max:
		c.Verdict = CalibrationTooEasy
	case rate < band.Min:
		c.Verdict = CalibrationTooHard
	}
	return c
}
