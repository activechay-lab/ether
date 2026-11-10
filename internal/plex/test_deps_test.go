package plex

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/sessions"
)

// testStores returns the real sessions-backed store factory for tests.
func testStores() frontend.PlexSessionStores { return sessions.NewPlexSessionStores() }

// testDeps fills the required host factories in d from the real
// sessions/experience implementations, leaving explicitly set fields alone.
func testDeps(d Deps) Deps {
	if d.SessionStores == nil {
		d.SessionStores = testStores()
	}
	return d
}

func TestNewServerRequiresSessionStores(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer without SessionStores did not panic")
		}
	}()
	NewServer(Deps{})
}
