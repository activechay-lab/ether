package nodewire

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

// Exercise the real consumer constructors and real Guard with the same boot
// before/after a live trust change, retaining the old transport throughout.
func TestOutboundConsumersCurrentTrust(t *testing.T) {
	for _, consumer := range []string{"source", "encoder"} {
		for _, change := range []string{"revoke", "roots", "expire"} {
			t.Run(consumer+"/"+change, func(t *testing.T) {
				now := time.Now()
				var clock atomic.Int64
				clock.Store(now.UnixNano())
				clockFn := func() time.Time { return time.Unix(0, clock.Load()) }
				ca1 := ca(t, now)
				local := Identity{"cluster", "controller", "controllerboot"}
				peer := Identity{"cluster", "worker", "workerboot"}
				var revoked atomic.Bool
				guard, err := NewGuard(snapshot(t, ca1, local, now), func(ctx context.Context, id Identity, _ *x509.Certificate) error {
					if ctx.Err() != nil || id != peer || revoked.Load() {
						return ErrDenied
					}
					return nil
				}, clockFn)
				if err != nil {
					t.Fatal(err)
				}
				sg, _ := NewGuard(snapshot(t, ca1, peer, now), func(context.Context, Identity, *x509.Certificate) error { return nil }, time.Now)
				grant := nodesource.Grant{Scope: nodesource.Scope{Fence: placement.Fence{Session: "session", Generation: 1, Controller: placement.Controller{ID: local.NodeID, Boot: local.Boot}, Target: placement.Target{NodeID: peer.NodeID, Boot: peer.Boot}}, Viewer: "viewer", Revision: "revision"}, Credential: "secret-source", Expires: now.Add(time.Minute)}
				scope, _ := json.Marshal(grant.Scope)
				var requests, connections, checks, callbacks atomic.Int64
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if consumer == "source" {
						if r.Header.Get(nodesource.CredentialHeader) != grant.Credential {
							t.Error("missing source credential")
						}
						w.Header().Set("ETag", fmt.Sprintf("\"%x\"", sha256.Sum256(scope)))
						w.Header().Set("Content-Length", "3")
						return
					}
					b, _ := io.ReadAll(r.Body)
					if len(b) == 0 {
						t.Error("missing execution body")
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(map[string]any{"State": "running", "Identity": remotetranscode.Identity{Controller: local.Boot, Worker: peer.Boot, Session: "session", Generation: 1}})
				}))
				server.TLS = sg.ServerTLS()
				server.Config.ConnState = func(_ net.Conn, s http.ConnState) {
					if s == http.StateNew {
						connections.Add(1)
					}
				}
				server.StartTLS()
				defer server.Close()
				tc, _ := guard.ClientTLS(peer, "127.0.0.1")
				// Isolate per-operation admission from handshake policy. Ordinary Go chain,
				// hostname and EKU checks still run before this preserved original callback.
				// Hold native TLS time steady in this fixture so expiry reaches the
				// per-operation recheck; clock-step handshakes are tested separately.
				tc.Time = time.Now
				tc.VerifyConnection = func(tls.ConnectionState) error { callbacks.Add(1); return nil }
				recheck := func(ctx context.Context, cs tls.ConnectionState) error {
					checks.Add(1)
					return guard.RecheckServer(ctx, cs, peer, "127.0.0.1")
				}
				var operation func() error
				if consumer == "source" {
					c, e := nodesource.NewClient(nodesource.ClientOptions{Endpoint: server.URL + nodesource.Path, TLS: tc, BindServer: func(tls.ConnectionState) error { return nil }, RecheckServer: recheck, Peer: nodesource.Peer{NodeID: peer.NodeID, Boot: peer.Boot}, MaxLifetime: time.Minute, Timeout: time.Second, Now: time.Now})
					if e != nil {
						t.Fatal(e)
					}
					defer c.Close()
					operation = func() error {
						r, e := c.Open(context.Background(), grant)
						if r != nil {
							r.Close()
						}
						return e
					}
				} else {
					decision := core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1}
					a, e := remotetranscode.NewAdapter(remotetranscode.AdapterOptions{Endpoint: server.URL, Client: &http.Client{Transport: &http.Transport{TLSClientConfig: tc}}, RecheckServer: recheck, Capacity: 1, Assign: func(context.Context, core.HLSRequest) (remotetranscode.Assignment, error) {
						return remotetranscode.Assignment{Identity: remotetranscode.Identity{Controller: local.Boot, Worker: peer.Boot, Session: "session", Generation: 1}, Grant: "secret-execution", Expires: now.Add(time.Minute)}, nil
					}, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision { return decision }})
					if e != nil {
						t.Fatal(e)
					}
					defer a.Close()
					operation = func() error {
						_, _, e := a.StartHLSOwned(context.Background(), core.HLSRequest{SessionID: "session", Decision: decision})
						return e
					}
				}
				if e := operation(); e != nil {
					t.Fatal("valid operation:", e)
				}
				// A second valid operation must also get a fresh connection.
				if e := operation(); e != nil {
					t.Fatal("second valid operation:", e)
				}
				if connections.Load() != 2 {
					t.Fatal("admitted connection reused")
				}
				switch change {
				case "revoke":
					revoked.Store(true)
				case "roots":
					if e := guard.Replace(snapshot(t, ca(t, now), local, now)); e != nil {
						t.Fatal(e)
					}
				case "expire":
					clock.Store(now.Add(2 * time.Hour).UnixNano())
				}
				if e := operation(); e == nil {
					t.Fatal("rejected peer accepted")
				}
				if requests.Load() != 2 || checks.Load() != 3 || callbacks.Load() != 3 || connections.Load() != 3 {
					t.Fatalf("requests=%d checks=%d original callbacks=%d connections=%d", requests.Load(), checks.Load(), callbacks.Load(), connections.Load())
				}
			})
		}
	}
}
