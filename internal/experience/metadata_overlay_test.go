package experience

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestMetadataOverlayRatingAllowed(t *testing.T) {
	kid := &core.User{ID: 2, MaxContentRating: "PG"}
	if !MetadataOverlayRatingAllowed(nil, "R", "R") {
		t.Fatal("nil viewer should allow")
	}
	if !MetadataOverlayRatingAllowed(&core.User{ID: 1}, "R", "R") {
		t.Fatal("unrestricted viewer should allow")
	}
	if !MetadataOverlayRatingAllowed(kid, "R", "") {
		t.Fatal("empty overlay rating should allow")
	}
	if !MetadataOverlayRatingAllowed(kid, "PG", "PG") {
		t.Fatal("matching cap should allow")
	}
	if !MetadataOverlayRatingAllowed(kid, "PG", "NR") {
		t.Fatal("rated catalogue item with unrated overlay keeps catalogue rating")
	}
	if MetadataOverlayRatingAllowed(kid, "PG", "R") {
		t.Fatal("overlay stricter rating exceeding cap must be rejected")
	}
	if MetadataOverlayRatingAllowed(kid, "", "NR") {
		t.Fatal("unrated catalogue and overlay must be rejected for capped viewer")
	}
}
