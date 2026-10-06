package resourceCalendarUseCase

import "strconv"

// formatMillicores is a CPU amount as text ("500m", "2").
func formatMillicores(m int64) string {
	if m%1000 == 0 {
		return strconv.FormatInt(m/1000, 10)
	}
	return strconv.FormatInt(m, 10) + "m"
}

// formatBytes is a memory amount as text in binary units ("512Mi", "4Gi").
func formatBytes(b int64) string {
	switch {
	case b >= 1<<30 && b%(1<<30) == 0:
		return strconv.FormatInt(b>>30, 10) + "Gi"
	case b >= 1<<20 && b%(1<<20) == 0:
		return strconv.FormatInt(b>>20, 10) + "Mi"
	case b >= 1<<20:
		return strconv.FormatInt(b>>20, 10) + "Mi"
	}
	return strconv.FormatInt(b, 10)
}
