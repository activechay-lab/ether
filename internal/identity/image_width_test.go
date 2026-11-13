package identity

import (
	"github.com/braedonsaunders/ether/internal/frontend"
	"net/http/httptest"
	"testing"
)

func TestGrantedImageWidthBudgets(t *testing.T) {
	for _, tc := range []struct {
		surface frontend.Protocol
		query   string
		want    int
	}{
		{"silo", "maxWidth=2688&quality=90", 2688},
		{"silo", "fillWidth=2560", 2560},
		{"silo", "maxWidth=99999", 3840},
		{"silo", "w=1920&maxWidth=2688", 1920},
		{"jellyfin", "maxWidth=2688", 2688},
		{"plex", "fillWidth=2560", 500},
		{"emby", "maxWidth=99999", 4000},
		{"jellyfin", "", 0},
		{"silo", "", 500},
		{"plex", "", 500},
		{"silo", "w=300", 300},
	} {
		if got := grantedImageWidth(httptest.NewRequest("GET", "/?"+tc.query, nil), tc.surface); got != tc.want {
			t.Errorf("%s %s: %d", tc.surface, tc.query, got)
		}
	}
}
