package identity_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

type grantPageExperience struct {
	frontendtest.Experience
	list func(context.Context, *core.Viewer, core.Device, []core.RatingKey) (map[core.RatingKey][]frontend.Image, error)
}

func (e grantPageExperience) ItemArtworkPage(ctx context.Context, v *core.Viewer, d core.Device, keys []core.RatingKey) (map[core.RatingKey][]frontend.Image, error) {
	return e.list(ctx, v, d, keys)
}
func (e grantPageExperience) ItemArtwork(context.Context, *core.Viewer, core.Device, core.RatingKey) ([]frontend.Image, bool, error) {
	panic("page authorization fell back to scalar artwork")
}

func TestImageGrantBatchPageAuthorization(t *testing.T) {
	for _, mode := range []string{"partial", "page-error", "invalid-image", "revoked-source", "inactive-member"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db := store.OpenTest(t)
			t.Logf("backend: %s", store.DialectOf(db))
			if _, err := db.ExecContext(ctx, `INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
				t.Fatal(err)
			}
			grants, err := identity.NewGrants(db, members{db}, time.Hour, nil)
			if err != nil {
				t.Fatal(err)
			}
			device := core.Device{ID: "tv"}
			credential, err := grants.Issue(ctx, 1, nil, "stremio", device)
			if err != nil {
				t.Fatal(err)
			}
			first, hidden, empty, extra := core.MovieKey(1), core.MovieKey(2), core.MovieKey(3), core.MovieKey(99)
			calls := 0
			exp := grantPageExperience{list: func(ctx context.Context, v *core.Viewer, d core.Device, keys []core.RatingKey) (map[core.RatingKey][]frontend.Image, error) {
				calls++
				if v.ID != 1 || d.ID != "tv" || !reflect.DeepEqual(keys, []core.RatingKey{first, hidden, empty}) {
					t.Fatalf("page request: %+v %+v %v", v, d, keys)
				}
				images := []frontend.Image{{Kind: frontend.ImageBackdrop, Path: "back"}, {Kind: frontend.ImagePoster, Path: "primary"}, {Kind: frontend.ImagePoster, Path: "alternate"}, {Kind: frontend.ImagePerson, Path: "person"}}
				switch mode {
				case "page-error":
					return nil, core.ErrNotAvailable
				case "invalid-image":
					images[0].Path = strings.Repeat("x", 1025)
				case "revoked-source":
					sum := sha256.Sum256([]byte(credential))
					if err := grants.RevokeGrant(ctx, 1, hex.EncodeToString(sum[:])); err != nil {
						t.Fatal(err)
					}
				case "inactive-member":
					if _, err := db.ExecContext(ctx, `UPDATE auth_users SET active=0 WHERE id=1`); err != nil {
						t.Fatal(err)
					}
				}
				// Hidden is omitted; an extra unrequested map key must never be granted.
				return map[core.RatingKey][]frontend.Image{first: images, empty: nil, extra: images}, nil
			}}
			service, err := identity.NewImageGrantService(db, "stremio", grants, func() frontend.Experience { return exp }, &imageFixture{}, time.Minute, nil)
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.IssueBatch(ctx, frontend.ImageBatchRequest{Credential: credential, Device: device, Items: []core.RatingKey{first, hidden, first, empty}, Kinds: []string{frontend.ImagePoster, frontend.ImageBackdrop}})
			if calls != 1 {
				t.Fatalf("page calls=%d", calls)
			}
			wantRows := 0
			if mode == "partial" {
				if err != nil || len(result) != 2 || result[0].Item != first || result[1].Item != empty || !result[0].Authoritative || !result[1].Authoritative || len(result[1].Images) != 0 {
					t.Fatalf("partial page: %+v %v", result, err)
				}
				if len(result[0].Images) != 2 || result[0].Images[0].Image.Path != "back" || result[0].Images[1].Image.Path != "primary" || result[0].Images[0].Secret == result[0].Images[1].Secret {
					t.Fatalf("selected images: %+v", result)
				}
				wantRows = 2
				// An empty request remains a no-op for artwork authorization.
				if empty, err := service.IssueBatch(ctx, frontend.ImageBatchRequest{Credential: credential, Device: device}); err != nil || len(empty) != 0 || calls != 1 {
					t.Fatalf("empty batch invoked page authorization: %v calls=%d", err, calls)
				}
			} else {
				want := core.ErrNotAvailable
				if mode == "revoked-source" || mode == "inactive-member" {
					want = core.ErrUnauthorized
				}
				if !errors.Is(err, want) || len(result) != 0 {
					t.Fatalf("failure must close issuance: %+v %v", result, err)
				}
			}
			for _, table := range []string{"frontend_image_grants", "capability_headers"} {
				var n int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil || n != wantRows {
					t.Fatalf("%s rows=%d want%d: %v", table, n, wantRows, err)
				}
			}
		})
	}
}
