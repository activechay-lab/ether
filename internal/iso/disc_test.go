package iso

import "testing"

func TestGroupKey(t *testing.T) {
	for _, tc := range []struct {
		label, file string
		key         string
		disc        int
	}{
		{"AVATAR_FIRE_AND_ASH_DISC_1", "a.iso", "AVATAR_FIRE_AND_ASH", 1},
		{"AVATAR_FIRE_AND_ASH_DISC_2", "b.iso", "AVATAR_FIRE_AND_ASH", 2},
		{"MOVIE-CD2", "x.iso", "MOVIE", 2},
		{"TITLE PART B", "x.iso", "TITLE", 2},
		{"", "Some.Movie.2024.Disc.2.iso", "SOME.MOVIE.2024", 2},
		{"SINGLE_DISC_TITLE", "x.iso", "SINGLE_DISC_TITLE", 0},
		{"", "", "", 0},
	} {
		if k, d := GroupKey(tc.label, tc.file); k != tc.key || d != tc.disc {
			t.Errorf("GroupKey(%q,%q) = %q,%d want %q,%d", tc.label, tc.file, k, d, tc.key, tc.disc)
		}
	}
}

func TestGroupKeyShortDOnlyTakesDigits(t *testing.T) {
	if k, d := GroupKey("SHOW_DS", ""); k != "SHOW_DS" || d != 0 {
		t.Fatalf("SHOW_DS -> %q %d", k, d)
	}
	if k, d := GroupKey("SHOW_D2", ""); k != "SHOW" || d != 2 {
		t.Fatalf("SHOW_D2 -> %q %d", k, d)
	}
}
