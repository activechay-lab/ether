package store

import ()

// UnixDay formats the Unix-seconds expression expr as a UTC 'YYYY-MM-DD'
// text value.
func UnixDay(d Dialect, expr string) string {
	if d == Postgres {
		return "to_char(to_timestamp(" + expr + ") AT TIME ZONE 'UTC','YYYY-MM-DD')"
	}
	return "strftime('%Y-%m-%d'," + expr + ",'unixepoch')"
}

// UnixHour formats the Unix-seconds expression expr as a zero-padded UTC
// hour of day ('00'..'23').
func UnixHour(d Dialect, expr string) string {
	if d == Postgres {
		return "to_char(to_timestamp(" + expr + ") AT TIME ZONE 'UTC','HH24')"
	}
	return "strftime('%H'," + expr + ",'unixepoch')"
}
