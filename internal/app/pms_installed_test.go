package app

import "testing"

func TestReservePMSPort(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		port                           int
		explicit, env, installed, want bool
	}{
		{"installed before PMS starts", 32400, false, false, true, true},
		{"PMS absent", 32400, false, false, false, false},
		{"owner selected default", 32400, true, false, true, false},
		{"environment wins", 32400, false, true, true, false},
		{"custom port", 32450, false, false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := reservePMSPort(tc.port, tc.explicit, tc.env, func() bool { calls++; return tc.installed })
			if got != tc.want {
				t.Fatalf("reserved = %v, want %v", got, tc.want)
			}
			if (tc.explicit || tc.env || tc.port != 32400) && calls != 0 {
				t.Fatal("unnecessary probe")
			}
		})
	}
}
