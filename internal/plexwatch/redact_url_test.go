package plexwatch

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func TestRedactMalformedURL(t *testing.T) {
	for _, raw := range []string{"https://host/%zz?X-Plex-Token=secret", "https://u:secret@host/path?X-Plex-Token=secret"} {
		err := redactURL(&url.Error{Op: "Get", URL: raw, Err: errors.New("transport failure")})
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("credential leaked: %v", err)
		}
	}
}
