package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/mcp"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestOAuthFlow(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	_, err := db.Exec(`INSERT INTO auth_users(id,plex_id,is_owner,active,created_at,updated_at) VALUES(1,100,1,1,0,0)`)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	grants, err := identity.NewGrants(db, testMembers{db}, 24*time.Hour, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	owner := true
	p, err := mcp.NewPairing(mcp.PairingOptions{DB: db, Grants: grants, Authorize: func(context.Context) (int64, error) {
		if !owner {
			return 0, errors.New("no session")
		}
		return 1, nil
	}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	oauth, console := p.OAuthHandler(), p.Handler()
	call := func(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "http://127.0.0.1:32400"+path, strings.NewReader(body))
		req.RemoteAddr = "10.0.0.1:1234"
		if method == "POST" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	decode := func(r *httptest.ResponseRecorder) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &m); err != nil {
			t.Fatalf("decode %s: %v", r.Body.String(), err)
		}
		return m
	}
	for _, path := range []string{"/.well-known/oauth-protected-resource/ether/mcp", "/.well-known/oauth-authorization-server"} {
		if r := call(oauth, "GET", path, ""); r.Code != 200 {
			t.Fatalf("metadata %s: %d", path, r.Code)
		}
	}
	invalid := call(oauth, "POST", "/ether/oauth/register", `{"redirect_uris":["http://evil.example/cb"]}`)
	if invalid.Code != 400 {
		t.Fatalf("invalid redirect: %d", invalid.Code)
	}
	registration := call(oauth, "POST", "/ether/oauth/register", `{"client_name":"Claude","redirect_uris":["http://127.0.0.1:1234/cb"]}`)
	if registration.Code != 201 {
		t.Fatalf("registration: %d %s", registration.Code, registration.Body)
	}
	client := decode(registration)["client_id"].(string)
	verifier := strings.Repeat("v", 43)
	h := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(h[:])
	redirect := "http://127.0.0.1:1234/cb"
	resource := "http://127.0.0.1:32400/ether/mcp"
	q := url.Values{"client_id": {client}, "redirect_uri": {redirect}, "response_type": {"code"}, "scope": {"read"}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "state": {"test"}, "resource": {resource}}
	auth := call(oauth, "GET", "/ether/oauth/authorize?"+q.Encode(), "")
	if auth.Code != 303 {
		t.Fatalf("authorize: %d %s", auth.Code, auth.Body)
	}
	loc, _ := url.Parse(auth.Header().Get("Location"))
	reqID := loc.Query().Get("request")
	owner = false
	denied := call(console, "GET", "/authorization?request="+reqID, "")
	if denied.Code != 403 {
		t.Fatalf("no owner: %d", denied.Code)
	}
	if w := call(console, "POST", "/authorization", `{"request":"`+reqID+`","approve":true}`); w.Code != 403 {
		t.Fatalf("no owner approval: %d", w.Code)
	}
	owner = true
	details := call(console, "GET", "/authorization?request="+reqID, "")
	if details.Code != 200 || decode(details)["name"] != "Claude" {
		t.Fatalf("details %d %s", details.Code, details.Body)
	}
	approval := call(console, "POST", "/authorization", `{"request":"`+reqID+`","approve":true}`)
	if approval.Code != 200 {
		t.Fatalf("approval: %d %s", approval.Code, approval.Body)
	}
	dest, _ := url.Parse(decode(approval)["redirect"].(string))
	code := dest.Query().Get("code")
	if code == "" || dest.Query().Get("state") != "test" {
		t.Fatal("missing code or state")
	}
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {client}, "redirect_uri": {redirect}, "resource": {resource}, "code": {code}, "code_verifier": {strings.Repeat("x", 43)}}
	if w := call(oauth, "POST", "/ether/oauth/token", form.Encode()); w.Code != 400 {
		t.Fatalf("wrong verifier: %d", w.Code)
	}
	form.Set("code_verifier", verifier)
	form.Set("redirect_uri", "http://127.0.0.1:1234/other")
	if w := call(oauth, "POST", "/ether/oauth/token", form.Encode()); w.Code != 400 {
		t.Fatalf("wrong redirect: %d", w.Code)
	}
	form.Set("redirect_uri", redirect)
	tokenResponse := call(oauth, "POST", "/ether/oauth/token", form.Encode())
	if tokenResponse.Code != 200 {
		t.Fatalf("token: %d %s", tokenResponse.Code, tokenResponse.Body)
	}
	token := decode(tokenResponse)["access_token"].(string)
	principal, err := p.Authenticate(ctx, token)
	if err != nil || !principal.HasScope("read") || principal.HasScope("full") {
		t.Fatalf("scope: %+v %v", principal, err)
	}
	if w := call(oauth, "POST", "/ether/oauth/token", form.Encode()); w.Code != 400 {
		t.Fatalf("code reuse: %d", w.Code)
	}
	// A denied request never produces a code, and retains the client's state.
	another := call(oauth, "GET", "/ether/oauth/authorize?"+q.Encode(), "")
	if another.Code != 303 {
		t.Fatalf("second authorize: %d", another.Code)
	}
	deniedURL, _ := url.Parse(another.Header().Get("Location"))
	decision := call(console, "POST", "/authorization", `{"request":"`+deniedURL.Query().Get("request")+`","approve":false}`)
	dst, _ := url.Parse(decode(decision)["redirect"].(string))
	if dst.Query().Get("error") != "access_denied" || dst.Query().Get("code") != "" || dst.Query().Get("state") != "test" {
		t.Fatalf("denial: %s", dst)
	}
	clients := decode(call(console, "GET", "/clients", ""))["clients"].([]any)
	if len(clients) != 1 {
		t.Fatal("missing client")
	}
	id := clients[0].(map[string]any)["id"].(string)
	if w := call(console, "DELETE", "/clients/"+id, ""); w.Code != 204 {
		t.Fatalf("revoke: %d", w.Code)
	}
	if _, err := p.Authenticate(ctx, token); err == nil {
		t.Fatal("revoked token accepted")
	}
	now = now.Add(time.Minute)
	expiredRequest := call(oauth, "GET", "/ether/oauth/authorize?"+q.Encode(), "")
	loc, _ = url.Parse(expiredRequest.Header().Get("Location"))
	expApproval := call(console, "POST", "/authorization", `{"request":"`+loc.Query().Get("request")+`","approve":true}`)
	expRedirect, _ := url.Parse(decode(expApproval)["redirect"].(string))
	form.Set("code", expRedirect.Query().Get("code"))
	now = now.Add(3 * time.Minute)
	if w := call(oauth, "POST", "/ether/oauth/token", form.Encode()); w.Code != 400 {
		t.Fatalf("expired code: %d", w.Code)
	}
	for i := 0; i < 10; i++ {
		_ = call(oauth, "POST", "/ether/oauth/register", `{"redirect_uris":["http://localhost:4567/cb"]}`)
	}
	if w := call(oauth, "POST", "/ether/oauth/register", `{"redirect_uris":["http://localhost:4567/cb"]}`); w.Code != 429 {
		t.Fatalf("registration rate limit: %d", w.Code)
	}

}

func TestOAuthRegistrationLimitAndRedirectPolicy(t *testing.T) {
	db := store.OpenTest(t)
	grants, err := identity.NewGrants(db, testMembers{db}, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := mcp.NewPairing(mcp.PairingOptions{DB: db, Grants: grants, Authorize: func(context.Context) (int64, error) { return 1, nil }})
	if err != nil {
		t.Fatal(err)
	}
	request := func(remote, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest("POST", "http://127.0.0.1/ether/oauth/register", strings.NewReader(body))
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		pairing.OAuthHandler().ServeHTTP(rec, req)
		return rec
	}
	for _, tc := range []struct {
		uri  string
		want int
	}{
		{"http://127.0.0.1:5678/callback", 201}, {"http://localhost:5678/callback", 201}, {"https://example.org/callback", 201},
		{"http://192.168.1.2/callback", 400}, {"http://localhost.evil.example/callback", 400}, {"http://user@localhost/callback", 400}, {"https://example.org/callback#fragment", 400},
	} {
		body, _ := json.Marshal(map[string]any{"redirect_uris": []string{tc.uri}})
		if got := request("127.0.0.1:1234", string(body)).Code; got != tc.want {
			t.Errorf("redirect %q: got %d, want %d", tc.uri, got, tc.want)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	stmt, err := tx.Prepare(`INSERT INTO mcp_oauth_clients(id,name,redirects,created) VALUES(?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4093; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("client-%d", i), "test", `["http://localhost/cb"]`, time.Now().UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if r := request("127.0.0.2:1234", `{"redirect_uris":["http://localhost/cb"]}`); r.Code != 429 {
		t.Fatalf("registration cap: %d %s", r.Code, r.Body)
	}
	// Once the seeded clients are old, the cap evicts unused ones, but never
	// a client holding a grant (even an expired one) or a pending request.
	far := time.Now().Add(time.Hour).UnixMilli()
	for _, q := range []string{
		`UPDATE mcp_oauth_clients SET created=0 WHERE id LIKE 'client-%'`,
		`INSERT INTO auth_users(id,plex_id,is_owner,active,created_at,updated_at) VALUES(1,100,1,1,0,0)`,
		`INSERT INTO identity_grants(hash,viewer_id,surface,device_id,client,product,version,created,last_used,expires) VALUES('g',1,'mcp','mcp-client-0','test','MCP','',0,0,1)`,
		fmt.Sprintf(`INSERT INTO mcp_oauth_requests(id,client_id,redirect_uri,state,challenge,scope,resource,expires) VALUES('r','client-1','http://localhost/cb','','','full','',%d)`, far),
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if r := request("127.0.0.3:1234", `{"redirect_uris":["http://localhost/cb"]}`); r.Code != 201 {
		t.Fatalf("registration after eviction: %d %s", r.Code, r.Body)
	}
	if _, err := mcp.PruneOAuth(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var kept, total int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mcp_oauth_clients WHERE id IN ('client-0','client-1')`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mcp_oauth_clients`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if kept != 2 || total != 6 {
		t.Fatalf("after prune kept=%d total=%d, want 2 and 6", kept, total)
	}
}
