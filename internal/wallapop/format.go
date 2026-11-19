package wallapop

import (
	"strconv"
	"time"
)

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// Timestamps from the API are epoch seconds or milliseconds, depending on the endpoint.

func msToTime(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339)
}

func secToTime(s int64) string {
	if s == 0 {
		return ""
	}
	return time.Unix(s, 0).UTC().Format(time.RFC3339)
}

func msToDate(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.DateOnly)
}

func msToMinute(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format("2006-01-02 15:04")
}
