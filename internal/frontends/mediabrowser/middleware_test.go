package mediabrowser

import (
	"context"
	"errors"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/watchparty"
)

func TestRouteAuthenticationCoverage(t *testing.T) {
	param := regexp.MustCompile(`\{[^}]+\}`)
	for _, f := range []frontend.Frontend{Jellyfin{MachineID: "test"}, Emby{MachineID: "test"}} {
		t.Run(string(f.Describe().ID), func(t *testing.T) {
			ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return nil, errors.New("invalid") }}
			inst, err := f.Mount(context.Background(), frontend.Host{Identity: ident, WatchParty: watchparty.New(watchparty.Options{})})
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close(context.Background())
			routes := inst.(*instance).Routes()
			for _, rt := range routes {
				if publicRoutesAllowlist[rt] {
					continue
				}
				path := param.ReplaceAllString(rt.Pattern, "testvalue")
				for _, prefix := range []string{"", "/" + string(f.Describe().ID)} {
					req := httptest.NewRequest(rt.Method, prefix+path, strings.NewReader(""))
					rec := httptest.NewRecorder()
					inst.Handler().ServeHTTP(rec, req)
					if rec.Code != 401 {
						t.Errorf("%s %s: got %d, want 401", rt.Method, prefix+rt.Pattern, rec.Code)
					}
				}
			}
		})
	}
}
