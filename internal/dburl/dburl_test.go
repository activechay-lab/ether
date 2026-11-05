package dburl

import (
	"strings"
	"testing"
)

// TestRedact pins the security invariant: no part of a password survives,
// whatever unencoded characters it holds.
func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		"": "",
		"postgres://u:pw@h:5432/db?sslmode=disable": "postgres://u:xxxxx@h:5432/db?sslmode=disable",
		"postgres://u@h/db":                         "postgres://u@h/db",
		"postgres://h/db?password=pw":               "postgres://h/db?password=xxxxx",
		"host=h user=u password=pw dbname=db":       "host=h user=u password=xxxxx dbname=db",
		"postgres://u:%23s%40c%3Ar%2Fet@h/db":       "postgres://u:xxxxx@h/db",
		"postgres://u:#secret@h/db":                 "postgres://u:xxxxx@h/db",
		"postgres://u:12#ab@h/db":                   "postgres://u:xxxxx@h/db",
		"postgres://u:p@ss#word@h/db":               "postgres://u:xxxxx@h/db",
		"postgres://u:p@ss@h/db":                    "postgres://u:xxxxx@h/db",
		"postgres://u:p:a:ss@h/db":                  "postgres://u:xxxxx@h/db",
		"postgres://u:pa/ss@h/db":                   "postgres://u:xxxxx@h/db",
		"postgres://u:12/34@h/db":                   "postgres://u:xxxxx@h/db",
		"postgres://u:12?34@h/db":                   "postgres://u:xxxxx@h/db",
		"postgres://u:a@b/c@h/db":                   "postgres://u:xxxxx@h/db",
		"postgres://h/db?password=a#bcd":            "postgres://h/db?password=xxxxx",
		"postgres://u:p#w@h/db?x=1&sslpassword=s#t": "postgres://u:xxxxx@h/db?x=1&sslpassword=xxxxx",
	} {
		got := Redact(in)
		if got != want {
			t.Errorf("Redact(%q) = %q, want %q", in, got, want)
		}
		for _, secret := range []string{"secret", "ss#word", "12#ab", "34", "bcd", "s#t", "%23s"} {
			if strings.Contains(in, secret) && strings.Contains(got, secret) {
				t.Errorf("Redact(%q) = %q leaks %q", in, got, secret)
			}
		}
	}
}
