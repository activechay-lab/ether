package plex

import (
	"testing"

	artworkservice "github.com/braedonsaunders/ether/internal/artwork/service"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/sessions"
)

// testStores returns the real sessions-backed store factory for tests.
func testStores() frontend.PlexSessionStores { return sessions.NewPlexSessionStores() }

// testArtworkStores returns the real artwork-backed store factory for tests.
func testArtworkStores() frontend.PlexArtworkStores { return artworkservice.NewPlexArtworkStores() }

// testDeps fills the required host factories in d from the real
// sessions/experience/artwork implementations, leaving explicitly set fields alone.
func testDeps(d Deps) Deps {
	if d.SessionStores == nil {
		d.SessionStores = testStores()
	}
	if d.Experience == nil {
		d.Experience = experience.NewPlexExperience()
	}
	if d.ArtworkStores == nil {
		d.ArtworkStores = testArtworkStores()
	}
	return d
}

func TestNewServerRequiresExperience(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer without Experience did not panic")
		}
	}()
	NewServer(Deps{SessionStores: testStores(), ArtworkStores: testArtworkStores()})
}

func TestNewServerRequiresSessionStores(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer without SessionStores did not panic")
		}
	}()
	NewServer(Deps{Experience: experience.NewPlexExperience(), ArtworkStores: testArtworkStores()})
}

func TestNewServerRequiresArtworkStores(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("NewServer without ArtworkStores did not panic")
		}
	}()
	NewServer(Deps{SessionStores: testStores(), Experience: experience.NewPlexExperience()})
}
