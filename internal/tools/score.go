package tools

import "math"

//const (
//	p0 = 0.7
//	p1 = 0.96
//)
//
//var (
//	c0 = -math.Atanh(p0)
//	c1 = math.Atanh(p1)
//)
//
//func dynA(solves float64) float64 {
//	return (1 - math.Tanh(solves)) / 2
//}
//
//func dynB(solves float64) float64 {
//	return (dynA((c1-c0)*solves+c0) - dynA(c1)) / (dynA(c0) - dynA(c1))
//}
//
//func CalculateScore(dMin, dMax, dSolveThreshold int32, solutions float64) int32 {
//	solutions = math.Max(0, solutions)
//	s := math.Max(1, float64(dSolveThreshold))
//	f := func(solutions float64) float64 {
//		return float64(dMin) + (float64(dMax)-float64(dMin))*dynB(solutions/s)
//	}
//	return int32(math.Round(math.Max(f(solutions), f(s))))
//}

func CalculateScore(dMin, dMax, dSolveThreshold int32, solutions float64) int32 {
	// solutions -1 because we don't want to count the current solution
	solutions = math.Max(0, solutions-1)
	s := math.Max(1, float64(dSolveThreshold))
	return int32(math.Max((float64(dMin-dMax)/(math.Pow(s, 2.0)))*math.Pow(solutions, 2.0)+float64(dMax), float64(dMin)))
}
