package releaseparse

import (
	"github.com/braedonsaunders/ether/internal/core"
)

// TitleYearMatches is TitleMatches plus a year check with ±1 tolerance.
// A zero year on either side skips the year check.
func TitleYearMatches(parsed core.ReleaseInfo, want string, wantYear int) bool {
	if !TitleMatches(parsed, want) {
		return false
	}
	return YearMatches(parsed.Year, wantYear)
}
