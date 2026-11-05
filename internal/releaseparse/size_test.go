package releaseparse

import "testing"

func TestParseSizeSharedUnits(t *testing.T) {
	for s, want := range map[string]int64{"1,234 MB": 1234 << 20, "1,234.5 MB": 1294467072, "1.234,5 MB": 1294467072, "1,5 GB": 1610612736, "1.5 GiB": 1610612736} {
		if got := ParseSize(s); got != want {
			t.Errorf("ParseSize(%q)=%d want %d", s, got, want)
		}
	}
}
