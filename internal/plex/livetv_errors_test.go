package plex

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/livetv"
)

func TestLiveTuneErrorsExplainLiveReasonInXMLAndJSON(t *testing.T) {
	f, _, _ := liveFixture(t)
	for _, format := range []string{"application/xml", "application/json"} {
		for _, tc := range []struct {
			err   error
			code  string
			retry string
		}{{livetv.ErrProviderBusy, "provider_busy", "5"}, {context.DeadlineExceeded, "timeout", "1"}, {errors.New("SECRET_RAW_PROVIDER_ERROR"), "source_unavailable", ""}} {
			r := httptest.NewRequest("POST", "/livetv/dvrs/1/channels/one/tune", nil)
			r.Header.Set("Accept", format)
			// Parse with the same negotiation middleware as a real request.
			r = r.WithContext(withRequest(r.Context(), ParseRequest(r)))
			w := httptest.NewRecorder()
			f.srv.writeLiveTuneError(w, r, tc.err)
			if w.Code != 503 || w.Header().Get("X-Ether-LiveTV-Error") != tc.code || w.Header().Get("Retry-After") != tc.retry {
				t.Fatalf("bad live failure: %d %v", w.Code, w.Header())
			}
			body := w.Body.String()
			if strings.Contains(body, "SECRET") || strings.Contains(body, "not available yet") || !strings.Contains(body, "message=") && !strings.Contains(body, "\"message\"") {
				t.Fatalf("missing safe live explanation: %s", body)
			}
		}
	}
}
