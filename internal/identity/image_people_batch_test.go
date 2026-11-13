package identity_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
)

type peopleBatchExperience struct {
	frontendtest.Experience
	authorize func([]frontend.PersonImageTarget) ([]bool, error)
}

func (e *peopleBatchExperience) AuthorizePersonImages(_ context.Context, _ *core.Viewer, _ core.Device, targets []frontend.PersonImageTarget) ([]bool, error) {
	return e.authorize(targets)
}

func TestPersonImageBatchAdmission(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if _, err := db.Exec(`INSERT INTO auth_users(id,plex_id,created_at,updated_at) VALUES(1,123,0,0)`); err != nil {
		t.Fatal(err)
	}
	grants, err := identity.NewGrants(db, members{db}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	device := core.Device{ID: "tv"}
	credential, err := grants.Issue(ctx, 1, nil, "silo", device)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	exp := &peopleBatchExperience{}
	exp.authorize = func(targets []frontend.PersonImageTarget) ([]bool, error) {
		calls++
		allowed := make([]bool, len(targets))
		for n := range targets {
			allowed[n] = n%2 == 0
		}
		return allowed, nil
	}
	service, err := identity.NewImageGrantService(db, "silo", grants, func() frontend.Experience { return exp }, &imageFixture{}, time.Minute, nil, identity.ImageGrantOptions{Token: authhdr.MediaBrowserToken, PublicContentTags: true})
	if err != nil {
		t.Fatal(err)
	}
	targets := make([]frontend.PersonImageTarget, 100)
	for n := range targets {
		targets[n] = frontend.PersonImageTarget{Item: core.MovieKey(int64(n + 1)), Image: frontend.Image{Kind: frontend.ImagePerson, Subject: fmt.Sprintf("tmdb:%d", n+1), Path: "/portrait"}}
	}
	secrets, err := service.IssuePersonImages(ctx, credential, device, targets)
	if err != nil || len(secrets) != 100 || calls != 1 {
		t.Fatalf("batch len=%d calls=%d err=%v", len(secrets), calls, err)
	}
	for n, secret := range secrets {
		if (secret != "") != (n%2 == 0) {
			t.Fatalf("denial/order at %d", n)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM frontend_image_grants`).Scan(&count); err != nil || count != 50 {
		t.Fatalf("payload count=%d %v", count, err)
	}
	if err := service.Revoke(ctx, credential, secrets[0]); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM frontend_image_grants`).Scan(&count); err != nil || count != 49 {
		t.Fatalf("independent revoke=%d %v", count, err)
	}
	if _, err := service.IssuePersonImages(ctx, credential, device, append(targets, targets[0])); !errors.Is(err, frontend.ErrImageBatchLimit) {
		t.Fatalf("oversize: %v", err)
	}
	exp.authorize = func(targets []frontend.PersonImageTarget) ([]bool, error) {
		if _, err := db.Exec(`UPDATE identity_grants SET revoked=1`); err != nil {
			return nil, err
		}
		allowed := make([]bool, len(targets))
		for n := range allowed {
			allowed[n] = true
		}
		return allowed, nil
	}
	if tokens, err := service.IssuePersonImages(ctx, credential, device, targets[:1]); err == nil || tokens != nil {
		t.Fatalf("source revoked during proof: %v %v", tokens, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM frontend_image_grants`).Scan(&count); err != nil || count != 49 {
		t.Fatalf("failed batch published payload=%d %v", count, err)
	}
}
