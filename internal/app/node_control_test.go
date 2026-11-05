package app

import (
	"net/http/httptest"
	"testing"
)

func TestNodeControlRequiresAuthenticatedOwnerContext(t *testing.T) {
	// Even an apparently local request cannot bypass the admin owner boundary.
	for _, method := range []string{"GET", "POST"} {
		rec := httptest.NewRecorder()
		(&App{}).nodeControlHandler().ServeHTTP(rec, httptest.NewRequest(method, "/nodes/n/control?boot=b", nil))
		if rec.Code != 401 {
			t.Fatalf("%s got %d", method, rec.Code)
		}
	}
}
