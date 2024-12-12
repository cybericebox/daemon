package tools

import "time"

func TimesEqual(t1, t2 time.Time) bool {
	return t1.Unix() == t2.Unix()
}
