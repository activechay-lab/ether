package app

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestCommunityUserEligibility(t *testing.T) {
	viewer := core.User{ID: 2}
	tests := []struct {
		name string
		row  auth.UserInfo
		want bool
	}{
		{"ordinary active shared viewer", auth.UserInfo{User: core.User{ID: 2}, Active: true, PlexShared: true}, true},
		{"inactive", auth.UserInfo{User: core.User{ID: 2}, Active: false, PlexShared: true}, false},
		{"not shared", auth.UserInfo{User: core.User{ID: 2}, Active: true, PlexShared: false}, false},
		{"owner", auth.UserInfo{User: core.User{ID: 2, IsOwner: true}, Active: true, PlexShared: true}, false},
		{"unrestricted Home friend", auth.UserInfo{User: core.User{ID: 2}, Active: true, PlexShared: true, Home: true, Restricted: false}, true},
		{"restricted Home profile", auth.UserInfo{User: core.User{ID: 2}, Active: true, PlexShared: true, Home: true, Restricted: true}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := communityUserEligible(&viewer, []auth.UserInfo{tc.row}); got != tc.want {
				t.Fatalf("eligible = %v, want %v", got, tc.want)
			}
		})
	}
}
