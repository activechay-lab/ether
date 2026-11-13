package app

import (
	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
)

func communityUserEligible(user *core.User, users []auth.UserInfo) bool {
	if user == nil || user.IsOwner || user.ID == 0 {
		return false
	}
	for _, candidate := range users {
		if candidate.ID == user.ID {
			return candidate.Active && candidate.PlexShared && !candidate.IsOwner && !(candidate.Home && candidate.Restricted)
		}
	}
	return false
}
