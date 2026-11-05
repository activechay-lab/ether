package transcode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

func TestDASHManifestIdentityDoesNotChangeEncoderOwnership(t *testing.T) {
	for _, public := range []string{"", "public-session"} {
		t.Run("public-"+public, func(t *testing.T) {
			m := newTestManager(t, newFakeRunner(), config.Transcode{})
			req := dashRequest(t, "exact-backend-execution", time.Minute)
			req.ManifestSessionID = public
			s := startDASHSession(t, m, req)
			out := httptest.NewRecorder()
			s.ServeManifest(out, httptest.NewRequest(http.MethodGet, "/start.mpd", nil))
			want := public
			if want == "" {
				want = req.SessionID
			}
			if out.Code != 200 || !strings.Contains(out.Body.String(), "session/"+want+"/$RepresentationID$/") {
				t.Fatalf("manifest lost public session URLs: %d %s", out.Code, out.Body.String())
			}
			if public != "" && strings.Contains(out.Body.String(), "session/"+req.SessionID+"/") {
				t.Fatal("private execution identity leaked into manifest")
			}
			if s.ID() != req.SessionID {
				t.Fatal("public manifest identity changed execution ownership")
			}
			if got, ok := m.Session(req.SessionID); !ok || got != s {
				t.Fatal("exact backend registry identity changed")
			}
			if public != "" {
				if _, ok := m.Session(public); ok {
					t.Fatal("public ID acquired backend registry ownership")
				}
			}
		})
	}
}
