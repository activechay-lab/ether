package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plexwatch"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPlexServersGlobalConfigSwitch(t *testing.T) {
	cfgStore := newStore(t, seededConfig)
	h := admin.NewHandler(admin.Deps{Config: cfgStore, IsOwnerToken: storedOwner(cfgStore)})
	for _, enabled := range []string{"false", "true"} {
		r := req(http.MethodPut, "/ether/api/config/plexservers", `{"enabled":`+enabled+`}`)
		r.Header.Set("X-Plex-Token", ownerToken)
		if res := serve(h, r); res.Code != http.StatusOK {
			t.Fatalf("%d: %s", res.Code, res.Body)
		}
		if cfgStore.Get().PlexServers.Enabled != (enabled == "true") {
			t.Fatal("flag not persisted")
		}
	}
}

func TestViewerPlexServersEndpoint(t *testing.T) {
	cfgStore := newStore(t, func(c *config.Config) {
		seededConfig(c)
		c.SetupComplete = true
	})
	db := store.OpenTest(t)
	st := state.New(db, state.Options{})
	svc, err := plexwatch.NewServers(plexwatch.ServersOptions{
		DB:     db,
		State:  st,
		Sealer: cfgStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	nextNS := int64(1700000600) * int64(time.Second)
	if _, err := db.Exec(`INSERT INTO plexwatch_pms(id,name,base_url,token,machine_id,problem) VALUES
		(1,'Living room PMS','http://192.168.1.10:32400','secret-srv-1','m1',''),
		(2,'Bedroom PMS','http://192.168.1.11:32400','secret-srv-2','m2','unreachable')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO plexwatch_pms_viewer(server_id,account_id,account_name,owner,user_id,token,direction,interval_min,last_import,last_send,next_run,problem) VALUES
		(1,1,'alex',1,7,'secret-acct-1',3,10,1700000100,1700000200,?,''),
		(2,4,'alex-kid',0,7,'secret-acct-2',0,30,1700000050,0,?,''),
		(1,2,'sam',0,8,'secret-acct-3',2,10,1700000300,0,?,'rate_limited')`,
		nextNS, nextNS, nextNS); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO plexwatch_pms_unmatched(server_id,user_id,dir,ref,title,reason,seen_at) VALUES
		(1,7,'in','9001','Home Video','no_match',1700000100)`); err != nil {
		t.Fatal(err)
	}

	h := admin.NewHandler(admin.Deps{
		Config:       cfgStore,
		IsOwnerToken: storedOwner(cfgStore),
		PlexServers:  svc,
		ViewerDirectory: func(context.Context) ([]admin.ViewerRecord, error) {
			return []admin.ViewerRecord{
				{User: core.User{ID: 7, PlexID: 99, Username: "alex"}},
				{User: core.User{ID: 8, PlexID: 0, Username: "sam"}},
				{User: core.User{ID: 9, PlexID: 101, Username: "unmapped"}},
			}, nil
		},
	})

	if res := serve(h, req(http.MethodGet, "/ether/api/viewers/7/plex-servers", "")); res.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d", res.Code)
	}

	r := req(http.MethodGet, "/ether/api/viewers/999/plex-servers", "")
	r.Header.Set("X-Plex-Token", ownerToken)
	if res := serve(h, r); res.Code != http.StatusNotFound {
		t.Fatalf("missing viewer status = %d: %s", res.Code, res.Body.String())
	}

	r = req(http.MethodGet, "/ether/api/viewers/9/plex-servers", "")
	r.Header.Set("X-Plex-Token", ownerToken)
	res := serve(h, r)
	if res.Code != http.StatusOK || strings.TrimSpace(res.Body.String()) != "[]" {
		t.Fatalf("unmapped viewer = %d %s", res.Code, res.Body.String())
	}

	r = req(http.MethodGet, "/ether/api/viewers/7/plex-servers", "")
	r.Header.Set("X-Plex-Token", ownerToken)
	res = serve(h, r)
	if res.Code != http.StatusOK {
		t.Fatalf("mapped viewer = %d %s", res.Code, res.Body.String())
	}
	if strings.Contains(res.Body.String(), "token") || strings.Contains(res.Body.String(), "secret-") {
		t.Fatalf("response leaked token data: %s", res.Body.String())
	}
	var got []plexwatch.PMSViewerMapping
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d mappings, want 2: %+v", len(got), got)
	}
	if got[0].ServerName != "Living room PMS" || got[0].AccountName != "alex" || !got[0].Owner ||
		got[0].Direction != 3 || got[0].Interval != 10 || got[0].LastImport != 1700000100 ||
		got[0].LastSend != 1700000200 || got[0].NextSync != 1700000600 || got[0].Problem != "" || got[0].Unmatched != 1 {
		t.Fatalf("mapping[0] = %+v", got[0])
	}
	// Direction 0 zeroes NextSync, and empty viewer problem inherits server problem.
	if got[1].ServerName != "Bedroom PMS" || got[1].Direction != 0 || got[1].NextSync != 0 || got[1].Problem != "unreachable" {
		t.Fatalf("mapping[1] = %+v", got[1])
	}
}

func TestPlexServerStepAsideEndpoint(t *testing.T) {
	cfgStore := newStore(t, func(c *config.Config) {
		seededConfig(c)
		c.SetupComplete = true
	})
	db := store.OpenTest(t)
	st := state.New(db, state.Options{})
	svc, err := plexwatch.NewServers(plexwatch.ServersOptions{
		DB:     db,
		State:  st,
		Sealer: cfgStore,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })

	if _, err := db.Exec(`INSERT INTO plexwatch_pms(id,name,base_url,token,machine_id,problem) VALUES
		(1,'Living room PMS','http://127.0.0.1:1','secret-srv-1','m1','')`); err != nil {
		t.Fatal(err)
	}

	h := admin.NewHandler(admin.Deps{
		Config:       cfgStore,
		IsOwnerToken: storedOwner(cfgStore),
		PlexServers:  svc,
	})

	if res := serve(h, req(http.MethodPut, "/ether/api/plex-servers/1/step-aside", `{"stepAside":true}`)); res.Code != http.StatusUnauthorized {
		t.Fatalf("unauth status = %d", res.Code)
	}

	r := req(http.MethodPut, "/ether/api/plex-servers/999/step-aside", `{"stepAside":true}`)
	r.Header.Set("X-Plex-Token", ownerToken)
	if res := serve(h, r); res.Code != http.StatusNotFound {
		t.Fatalf("missing server status = %d: %s", res.Code, res.Body.String())
	}

	for _, want := range []bool{true, false} {
		body := `{"stepAside":false}`
		if want {
			body = `{"stepAside":true}`
		}
		r := req(http.MethodPut, "/ether/api/plex-servers/1/step-aside", body)
		r.Header.Set("X-Plex-Token", ownerToken)
		res := serve(h, r)
		if res.Code != http.StatusOK {
			t.Fatalf("put stepAside=%v: %d %s", want, res.Code, res.Body.String())
		}
		var srv plexwatch.PMSServer
		if err := json.Unmarshal(res.Body.Bytes(), &srv); err != nil {
			t.Fatal(err)
		}
		if srv.StepAside != want {
			t.Fatalf("got stepAside=%v, want %v", srv.StepAside, want)
		}
	}
}
