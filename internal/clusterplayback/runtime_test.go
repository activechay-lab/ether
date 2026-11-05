package clusterplayback

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/playbackcontinuity"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"github.com/braedonsaunders/ether/internal/sessions"
	"github.com/braedonsaunders/ether/internal/store"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureCatalog struct{ core.Catalog }

func (fixtureCatalog) Get(_ context.Context, k core.RatingKey) (*core.Title, error) {
	return &core.Title{Key: k, Type: core.TypeMovie, Title: "Fixture"}, nil
}

type fixtureResolver struct{ core.Resolver }

func (fixtureResolver) Resolve(_ context.Context, q core.ResolveRequest) (*core.Resolution, error) {
	return &core.Resolution{Title: q.Title.Key, Release: core.Release{ID: "fixture", Name: "fixture.mkv"}, NZB: []byte("offline")}, nil
}

type fixtureEngine struct{ core.Engine }

func (fixtureEngine) Open(context.Context, string, []byte) (core.MediaSource, error) {
	return fixtureBytes{}, nil
}
func (fixtureEngine) Prefetch(context.Context, string, []byte, int64, int64) error { return nil }

type fixtureBytes struct{}

func (fixtureBytes) Size() int64       { return 10 }
func (fixtureBytes) Name() string      { return "fixture.mkv" }
func (fixtureBytes) ReleaseID() string { return "fixture" }
func (fixtureBytes) Close() error      { return nil }
func (fixtureBytes) ReadAt(b []byte, off int64) (int, error) {
	return strings.NewReader("0123456789").ReadAt(b, off)
}
func (f fixtureBytes) ReadAtContext(_ context.Context, b []byte, off int64) (int, error) {
	return f.ReadAt(b, off)
}

type fixtureEncoder struct {
	core.Transcoder
	session *fixtureSession
}

func (*fixtureEncoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	return &core.MediaInfo{Size: 10, Container: "mkv", Duration: time.Minute, Streams: []core.StreamInfo{{Type: "video", Codec: "h264", Height: 720}}}, nil
}
func (f *fixtureEncoder) StartHLSOwned(ctx context.Context, q core.HLSRequest) (core.HLSSession, core.EncoderRetirement, error) {
	if e := q.InputLease.ClaimInput(func(error) {}); e != nil {
		return nil, nil, e
	}
	headers, e := q.InputLease.HeadersForInput(q.InputURL)
	if e != nil {
		return nil, nil, e
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", q.InputURL, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	response, e := http.DefaultClient.Do(req)
	if e != nil {
		return nil, nil, e
	}
	defer response.Body.Close()
	b, e := io.ReadAll(response.Body)
	if e != nil || string(b) != "0123456789" {
		return nil, nil, ErrDenied
	}
	s := &fixtureSession{id: q.SessionID, input: q.InputLease, done: make(chan struct{})}
	f.session = s
	return s, s, nil
}
func (f *fixtureEncoder) StartHLS(c context.Context, q core.HLSRequest) (core.HLSSession, error) {
	s, _, e := f.StartHLSOwned(c, q)
	return s, e
}
func (f *fixtureEncoder) Session(string) (core.HLSSession, bool) { return f.session, f.session != nil }
func (f *fixtureEncoder) Stop(string) {
	if f.session != nil {
		_ = f.session.Close()
	}
}

type fixtureSession struct {
	id    string
	input core.EncoderInputLease
	done  chan struct{}
	once  sync.Once
}

func (s *fixtureSession) ID() string            { return s.id }
func (s *fixtureSession) LastAccess() time.Time { return time.Now() }
func (s *fixtureSession) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	_, _ = io.WriteString(w, "#EXTM3U\nsegment-0.ts\n")
}
func (s *fixtureSession) ServeSegment(w http.ResponseWriter, r *http.Request, n int) {
	w.Header().Set("ETag", "\"fixture\"")
	http.ServeContent(w, r, "segment.ts", time.Time{}, strings.NewReader("segment"))
}
func (s *fixtureSession) SwapInput(context.Context, string, core.MediaInfo, time.Duration) error {
	return ErrDenied
}
func (s *fixtureSession) Close() error { return s.RetireEncoder() }
func (s *fixtureSession) RetireEncoder() error {
	s.once.Do(func() { s.input.SealInput(); close(s.done) })
	return nil
}
func (s *fixtureSession) EncoderDone() <-chan struct{} { return s.done }
func (s *fixtureSession) EncoderErr() error {
	select {
	case <-s.done:
		return nil
	default:
		return core.ErrEncoderPending
	}
}
func (s *fixtureSession) EncoderRetirement() core.EncoderRetirement { return s }

func TestSourceWorkerDeliveryAndExactRetirement(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := store.OpenTest(t)
	v := NewVerifier()
	p, e := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: 10 * time.Minute, LockTimeout: time.Second}, v)
	if e != nil {
		t.Fatal(e)
	}
	encoder := &fixtureEncoder{}
	manager, e := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{}, Transcoder: encoder, SelectProfile: func(core.ClientCaps, *core.User) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	ps, _, e := manager.PrepareSurfaceSource(ctx, &core.User{ID: 9}, core.MovieKey(1), core.ClientCaps{ClientID: "offline"}, "", false)
	if e != nil {
		t.Fatal(e)
	}
	access, e := playback.NewSourceAccess(manager, 16, 16)
	if e != nil {
		t.Fatal(e)
	}
	sourceID := nodewire.Identity{Cluster: "offline", NodeID: "source", Boot: "source-boot"}
	workerID := nodewire.Identity{Cluster: "offline", NodeID: "worker", Boot: "worker-boot"}
	frontendID := nodewire.Identity{Cluster: "offline", NodeID: "frontend", Boot: "frontend-boot"}
	brokerID := nodewire.Identity{Cluster: "offline", NodeID: "broker", Boot: "broker-boot"}
	deliveryID := nodewire.Identity{Cluster: "offline", NodeID: "delivery", Boot: "delivery-boot"}
	guards := fixtureGuards(t, sourceID, workerID, frontendID, brokerID, deliveryID)
	endpoints := map[nodewire.Identity]string{}
	endpoint := func(_ context.Context, id nodewire.Identity) (string, error) {
		s, ok := endpoints[id]
		if !ok {
			return "", ErrDenied
		}
		return s, nil
	}
	eligible := func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }
	base := Options{DB: db, Placement: p, Endpoint: endpoint, Eligible: eligible, MemberRole: eligible, Capacity: 16, ClockBoundKnown: true, ClockSkew: time.Millisecond, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision { return core.Decision{} }}
	o := base
	o.Self = sourceID
	o.Guard = guards[0]
	o.Playback = manager
	o.SourceAccess = access
	o.SourcePolicy = func(_ context.Context, _ *core.Viewer, ps *core.PlaySession) error {
		if strings.Contains(ps.ClientID, "owned-error") {
			return ErrDenied
		}
		return nil
	}
	o.ActiveViewer = func(_ context.Context, id int64) (*core.Viewer, error) {
		if id != 9 {
			return nil, ErrDenied
		}
		return &core.Viewer{ID: id}, nil
	}
	source, e := NewRuntime(o)
	if e != nil {
		t.Fatal(e)
	}
	defer source.Close()

	o = base
	o.Self = workerID
	o.Guard = guards[1]
	o.Encoder = encoder
	worker, e := NewRuntime(o)
	if e != nil {
		t.Fatal(e)
	}
	defer worker.Close()
	o = base
	o.Self = frontendID
	o.Guard = guards[2]
	front, e := NewRuntime(o)
	if e != nil {
		t.Fatal(e)
	}
	defer front.Close()
	if e = v.Bind(front); e != nil {
		t.Fatal(e)
	}
	for i, r := range []*Runtime{source, worker, front} {
		server := httptest.NewUnstartedServer(r.Handler())
		server.TLS = guards[i].ServerTLS()
		server.StartTLS()
		defer server.Close()
		endpoints[r.o.Self] = server.URL
	}
	testPreparationFailures(t, ctx, front, source, sourceID, frontendID)
	prepared, err := front.PrepareSource(ctx, sourceID, PrepareRequest{Operation: "prepare-offline", Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: "offline"}, Protocol: "hls"})
	if err != nil || prepared.Owner != sourceID || prepared.PlayID != ps.ID || prepared.Revision == "" {
		t.Fatalf("remote prepare: %#v %v", prepared, err)
	}

	// A proven pre-reservation failure must not poison a stable public ID.
	preflight := core.HLSRequest{SessionID: "public-session", Info: ps.Info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1}}
	if _, err := front.StartPreparedHLS(ctx, 9, prepared, preflight); err == nil || front.Allocated(preflight.SessionID) {
		t.Fatal("no-target failure retained ownership")
	}
	target := placement.Target{NodeID: workerID.NodeID, Boot: workerID.Boot}
	if e = p.SetPool(ctx, "worker/streams", config.RoleTranscode, 1); e != nil {
		t.Fatal(e)
	}
	if e = p.SetTarget(ctx, target, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"worker/streams"}}); e != nil {
		t.Fatal(e)
	}
	testRemoteNeutralBroker(t, ctx, base, guards[3], brokerID, sourceID, endpoints, source, worker)
	request := core.HLSRequest{SessionID: "public-session", Info: ps.Info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1}}
	h, e := front.StartPreparedHLS(ctx, 9, prepared, request)
	if e != nil {
		t.Fatal(e)
	}
	response := httptest.NewRecorder()
	h.ServeSegment(response, httptest.NewRequest("GET", "https://public/sessions/media/stable/segment", nil), 0)
	if response.Code != 200 || response.Body.String() != "segment" {
		t.Fatalf("delivery %d %s", response.Code, response.Body.String())
	}
	// Source rotation and worker acceptance share one exact renewal history.
	remote := h.(*remotetranscode.Session)
	lease := remote.Lease()
	deadline := time.Now().Add(3 * time.Minute).UTC().Truncate(time.Millisecond)
	record, err := p.Lookup(ctx, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Renew(ctx, record.Fence, deadline.Add(2*source.o.ClockSkew)); err != nil {
		t.Fatal(err)
	}
	renewal := remotetranscode.Renewal{Identity: remoteIdentity(Route{Fence: record.Fence}), Operation: 1, PreviousVersion: 0, PreviousDeadline: lease.Deadline, Deadline: deadline}
	if _, err = remote.Renew(ctx, renewal); err != nil {
		t.Fatal(err)
	}
	if _, err = remote.Renew(ctx, renewal); err != nil {
		t.Fatal("accepted renewal replay", err)
	}
	// Any frontend forwards by session index and owning broker rechecks grants.
	if err = front.BindDelivery(func(w http.ResponseWriter, q *http.Request, protocol string) {
		if !strings.Contains(q.URL.Path, "/grant/") {
			http.NotFound(w, q)
			return
		}
		h.ServeSegment(w, q, 0)
	}); err != nil {
		t.Fatal(err)
	}
	forwarded := httptest.NewRecorder()
	forwardRequest := httptest.NewRequest("GET", "https://public/sessions/media/public-session/grant/segment", nil)
	forwardRequest.Header.Set("Range", "bytes=1-3")
	if !worker.ForwardMedia(forwarded, forwardRequest, request.SessionID, "") || forwarded.Code != 206 || forwarded.Body.String() != "egm" {
		t.Fatalf("forward %d %s", forwarded.Code, forwarded.Body.String())
	}
	// Plex representation forwarding is a separate typed media scope; the
	// original owner handler still validates its public token/grant.
	if err = front.BindPlexDelivery(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		token := q.Header.Get("X-Plex-Token")
		if token != "offline-plex-token" && token != "offline-restricted-plex-token" {
			http.Error(w, "denied", 401)
			return
		}
		if q.Host != "public" || q.URL.Scheme != "https" {
			http.Error(w, "origin changed", 400)
			return
		}
		if token == "offline-restricted-plex-token" {
			_, lan := identity.ClientNet(q, netip.MustParsePrefix("10.1.0.0/16"))
			if !lan {
				http.Error(w, "remote restricted", 403)
				return
			}
		}
		h.ServeSegment(w, q, 0)
	})); err != nil {
		t.Fatal(err)
	}
	plexRequest := httptest.NewRequest("GET", "https://public/video/:/transcode/universal/session/public-session/base/0.ts", nil)
	plexResponse := httptest.NewRecorder()
	if !worker.ForwardPlexMedia(plexResponse, plexRequest, request.SessionID) || plexResponse.Code != 401 {
		t.Fatal("Plex forwarding bypassed original authorization")
	}
	plexRequest.Header.Set("X-Plex-Token", "offline-plex-token")
	plexRequest.Header.Set("Range", "bytes=1-3")
	plexResponse = httptest.NewRecorder()
	if !worker.ForwardPlexMedia(plexResponse, plexRequest, request.SessionID) || plexResponse.Code != 206 || plexResponse.Body.String() != "egm" {
		t.Fatalf("Plex range proxy %d %q", plexResponse.Code, plexResponse.Body.String())
	}
	if worker.ForwardPlexMedia(httptest.NewRecorder(), httptest.NewRequest("GET", "https://public/video/:/transcode/universal/start.m3u8?session=public-session", nil), request.SessionID) {
		t.Fatal("control URI admitted to media scope")
	}
	for _, test := range []struct {
		name, remote, xff string
		status            int
	}{{"directWAN", "203.0.113.7:1234", "", 403}, {"directLAN", "192.168.1.7:1234", "", 200}, {"trustedproxyWAN", "10.1.0.2:1234", "203.0.113.7", 403}, {"trustedproxyLAN", "10.1.0.2:1234", "192.168.1.7", 200}, {"untrustedproxyspoof", "203.0.113.7:1234", "192.168.1.7", 403}} {
		req := httptest.NewRequest("GET", "https://public/video/:/transcode/universal/session/public-session/base/0.ts", nil)
		req.RemoteAddr = test.remote
		req.Header.Set("X-Plex-Token", "offline-restricted-plex-token")
		if test.xff != "" {
			req.Header.Set("X-Forwarded-For", test.xff)
		}
		req.Header.Set(deliveryOrigin, `{"RemoteAddr":"127.0.0.1:1","Host":"private","Scheme":"http"}`)
		response := httptest.NewRecorder()
		if !worker.ForwardPlexMedia(response, req, request.SessionID) || response.Code != test.status {
			t.Fatalf("provenance %s %d %q", test.name, response.Code, response.Body.String())
		}
	}
	deliveryOptions := base
	deliveryOptions.Self = deliveryID
	deliveryOptions.Guard = guards[4]
	deliveryOptions.Roles = config.NodeRoles{config.RoleDelivery}
	delivery, err := NewRuntime(deliveryOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer delivery.Close()
	if delivery.Ready() {
		t.Fatal("delivery-only ready before actual public handler")
	}
	public, err := delivery.NewPublicDelivery(func(_ context.Context, q *http.Request, _ string) error {
		if q.Header.Get("X-Plex-Token") != "offline-plex-token" {
			return ErrDenied
		}
		return nil
	})
	if err != nil || !delivery.Ready() {
		t.Fatal("delivery-only sender not ready", err)
	}
	publicResponse := httptest.NewRecorder()
	public.ServeHTTP(publicResponse, httptest.NewRequest("HEAD", "https://public/sessions/media/public-session/grant/segment", nil))
	if publicResponse.Code != 200 || publicResponse.Body.Len() != 0 {
		t.Fatalf("delivery-only HEAD %d %q", publicResponse.Code, publicResponse.Body.String())
	}
	publicResponse = httptest.NewRecorder()
	public.ServeHTTP(publicResponse, httptest.NewRequest("GET", "https://public/video/:/transcode/universal/session/public-session/base/0.ts", nil))
	if publicResponse.Code != 401 {
		t.Fatal("delivery-only Plex sender skipped valid-token receipt policy")
	}
	// Durable capacity and exact identity survive a duplicate start.
	if _, e = front.StartPreparedHLS(ctx, 9, prepared, request); e == nil {
		t.Fatal("duplicate admitted")
	}
	front.Stop(request.SessionID)
	token, owned := front.EncoderRetirement(request.SessionID)
	if !owned || token == nil {
		t.Fatal("stop discarded remote ownership")
	}
	if _, available := front.Session(request.SessionID); available {
		t.Fatal("stopped execution advertised available")
	}
	if token.EncoderErr() == nil {
		t.Fatal("stop alone proved retirement")
	}
	select {
	case <-token.EncoderDone():
		t.Fatal("token closed before dual retirement")
	default:
	}
	for i := 0; i < 50; i++ {
		worker.Cleanup()
		source.Cleanup()
		_ = front.Tick(ctx)
		record, e := p.Lookup(ctx, request.SessionID)
		if e == nil && record.Retired {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	record, err = p.Lookup(ctx, request.SessionID)
	if err != nil || !record.Retired {
		t.Fatal("exact retirement did not release placement")
	}
	// Same-owner reopen requires both old retirements and fresh exact revision
	// and size; its attestation then binds a currently authorized new fence.
	oldBinding := playbackcontinuity.SourceBinding{Owner: placement.Target{NodeID: sourceID.NodeID, Boot: sourceID.Boot}, PlayID: prepared.PlayID, Revision: prepared.Revision, Fence: record.Fence}
	reopened, err := front.ReopenSource(ctx, oldBinding, "same-owner-reopen")
	if err != nil || reopened.Proof == "" || reopened.Prepared.Revision != oldBinding.Revision || reopened.Prepared.Info.Size != prepared.Info.Size {
		t.Fatal("same-owner reopen", err)
	}
	restarted, err := front.StartPreparedHLS(ctx, 9, reopened.Prepared, request)
	if err != nil {
		t.Fatal("reopened generation", err)
	}
	nextRecord, err := p.Lookup(ctx, request.SessionID)
	if err != nil || nextRecord.Generation <= record.Generation {
		t.Fatal("missing new fence", err)
	}
	nextBinding := playbackcontinuity.SourceBinding{Owner: oldBinding.Owner, PlayID: reopened.Prepared.PlayID, Revision: reopened.Prepared.Revision, Fence: nextRecord.Fence}
	if err = front.VerifyReopen(ctx, oldBinding, nextBinding, reopened.Proof); err != nil {
		t.Fatal("reopen attestation", err)
	}
	changed := nextBinding
	changed.Revision = "changed"
	if err = front.VerifyReopen(ctx, oldBinding, changed, reopened.Proof); err == nil {
		t.Fatal("changed revision accepted")
	}
	_ = restarted
	front.Stop(request.SessionID)
	for i := 0; i < 50; i++ {
		worker.Cleanup()
		source.Cleanup()
		_ = front.Tick(ctx)
		nextRecord, err = p.Lookup(ctx, request.SessionID)
		if err == nil && nextRecord.Retired {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !nextRecord.Retired {
		t.Fatal("reopened execution did not retire")
	}
	if err = front.VerifyReopen(ctx, oldBinding, nextBinding, reopened.Proof); err == nil {
		t.Fatal("historical reopen proof revived retired authority")
	}
	// Reconcile a committed reservation whose response was lost. No encoder
	// was contacted; retry must use the identical immutable operation.
	prepared2, err := front.PrepareSource(ctx, sourceID, PrepareRequest{Operation: "prepare-uncertain", Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: "uncertain-client"}, Protocol: "hls"})
	if err != nil {
		t.Fatal(err)
	}
	reserve := placement.Request{Session: "uncertain-reserve", Operation: "same-immutable-operation", Controller: placement.Controller{ID: frontendID.NodeID, Boot: frontendID.Boot}, Target: target, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: "worker/streams", Units: 1}}, Lease: 2 * time.Minute}
	committed, err := p.Reserve(ctx, reserve)
	if err != nil {
		t.Fatal(err)
	}
	front.executions[reserve.Session] = &execution{request: reserve, reservePending: true, route: Route{Fence: committed.Fence, Source: sourceID, Viewer: 9, PlayID: prepared2.PlayID, Revision: prepared2.Revision}}
	for i := 0; i < 50; i++ {
		_ = front.Tick(ctx)
		source.Cleanup()
		rec, e := p.Lookup(ctx, reserve.Session)
		if e == nil && rec.Retired {
			if rec.Operation != reserve.Operation || len(worker.encoderTokens) != 3 {
				t.Fatal("reconciliation changed operation or started another encoder")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("unknown reserve did not reconcile exact retirement")
}
func fixtureGuards(t *testing.T, ids ...nodewire.Identity) []*nodewire.Guard {
	t.Helper()
	now := time.Now()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "offline"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	roots := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	out := make([]*nodewire.Guard, 0, len(ids))
	for _, id := range ids {
		_, leaf, _ := ed25519.GenerateKey(rand.Reader)
		csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, leaf)
		cert, e := nodewire.Issue(der, key, csr, id, []string{"127.0.0.1"}, now, time.Hour)
		if e != nil {
			t.Fatal(e)
		}
		priv, _ := x509.MarshalPKCS8PrivateKey(leaf)
		snapshot, e := nodewire.NewSnapshot(id, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: priv}), roots, now)
		if e != nil {
			t.Fatal(e)
		}
		g, e := nodewire.NewGuard(snapshot, func(context.Context, nodewire.Identity, *x509.Certificate) error { return nil }, time.Now)
		if e != nil {
			t.Fatal(e)
		}
		out = append(out, g)
	}
	return out
}

type fixtureProgress struct{}

func (fixtureProgress) Progress(context.Context, int64, core.RatingKey, time.Duration, time.Duration, string) error {
	return nil
}
func testRemoteNeutralBroker(t *testing.T, ctx context.Context, base Options, guard *nodewire.Guard, self, sourceID nodewire.Identity, endpoints map[nodewire.Identity]string, source, worker *Runtime) {
	t.Helper()
	verifier := NewVerifier()
	ownPlacement, err := placement.New(base.DB, placement.Options{MaxSessions: 16, MaxLease: 10 * time.Minute, LockTimeout: time.Second}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	o := base
	o.Placement = ownPlacement
	o.Self = self
	o.Guard = guard
	o.Roles = config.NodeRoles{config.RoleServer, config.RoleDelivery}
	o.Decide = func(i core.MediaInfo, c core.ClientCaps, d core.DecideOptions) core.Decision {
		return core.Decision{Kind: core.DecisionDirectPlay, VideoCopy: true, AudioCopy: true, Container: i.Container, AudioStream: -1, SubtitleStream: -1}
	}
	runtime, err := NewRuntime(o)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	if err = verifier.Bind(runtime); err != nil {
		t.Fatal(err)
	}
	remote, err := NewRemotePlayback(runtime, RemotePlaybackOptions{Estimate: func(context.Context, *core.Title, core.ClientCaps) core.MediaInfo { return core.MediaInfo{} }, SelectSource: func(context.Context) (nodewire.Identity, error) { return sourceID, nil }, Capacity: 16})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := runtime.DecisionTranscoder()
	if err != nil {
		t.Fatal(err)
	}
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := sessions.NewEventBus(nil, active)
	defer events.Close()
	broker, err := sessions.NewBroker(sessions.BrokerDeps{Playback: remote, Transcoder: decision, DistributedHLS: runtime, Events: events, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, Progress: fixtureProgress{}, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k, Type: core.TypeMovie}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(ctx)
	server := httptest.NewUnstartedServer(runtime.Handler())
	server.TLS = guard.ServerTLS()
	server.StartTLS()
	defer server.Close()
	endpoints[self] = server.URL
	viewer := &core.Viewer{ID: 9}
	device := core.Device{ID: "remote-neutral-device"}
	plan, err := broker.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}}})
	if err != nil {
		t.Fatal("remote neutral plan", err)
	}
	if plan.Decision.Kind != core.DecisionDirectPlay || plan.ETag == "" {
		t.Fatalf("changed direct decision or missing revision: %+v", plan)
	}
	opened, err := broker.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: "jellyfin", PlanID: plan.ID})
	if err != nil {
		t.Fatal("remote neutral open", err)
	}
	access := frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: "jellyfin"}
	localSources := remote.Sessions()
	if len(localSources) != 1 {
		t.Fatal("missing local remote-source metadata")
	}
	captured, ok := remote.prepared(localSources[0].ID)
	if !ok {
		t.Fatal("missing exact prepared owner")
	}
	ownedSource, ok := source.o.Playback.Session(captured.PlayID)
	if !ok || ownedSource.PreparedOnly {
		t.Fatal("broker open did not activate source owner")
	}
	if err = broker.Report(ctx, access, opened.ID, frontend.Timeline{State: "paused", Position: time.Second}); err != nil {
		t.Fatal(err)
	}
	if err = runtime.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	ownedSource, ok = source.o.Playback.Session(captured.PlayID)
	if !ok || ownedSource.ViewerState != "paused" {
		t.Fatal("paused activity did not reach exact source owner")
	}

	path, err := broker.StartStream(ctx, access, opened.ID, frontend.StreamRequest{Format: frontend.StreamDirect})
	if err != nil {
		t.Fatal("remote neutral direct", err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "https://public"+path, nil)
	request.Header.Set("Range", "bytes=2-5")
	broker.ServeMedia(response, request)
	if response.Code != 206 || response.Body.String() != "2345" {
		t.Fatalf("remote direct bytes %d %q", response.Code, response.Body.String())
	}
	forwarded := httptest.NewRecorder()
	request = httptest.NewRequest("HEAD", "https://public"+path, nil)
	if !source.ForwardMedia(forwarded, request, opened.ID, "jellyfin") || forwarded.Code != 200 || forwarded.Body.Len() != 0 {
		t.Fatalf("remote direct indexed HEAD %d %q", forwarded.Code, forwarded.Body.String())
	}
	hlsPath, err := broker.StartStream(ctx, access, opened.ID, frontend.StreamRequest{Format: frontend.StreamHLS})
	if err != nil {
		t.Fatal("remote neutral HLS", err)
	}
	playlist := httptest.NewRecorder()
	broker.ServeMedia(playlist, httptest.NewRequest("GET", "https://public"+hlsPath, nil))
	if playlist.Code != 200 || !strings.Contains(playlist.Body.String(), "#EXTM3U") {
		t.Fatalf("remote neutral HLS playlist %d %q", playlist.Code, playlist.Body.String())
	}
	if err = broker.Stop(ctx, access, opened.ID); err != nil {
		t.Fatal("remote stop", err)
	}
	for i := 0; i < 50; i++ {
		_ = runtime.Tick(ctx)
		source.Cleanup()
		worker.Cleanup()
		if n, e := runtime.Pending(ctx); e == nil && n == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("remote plan lifetime did not retire")
}

func (*fixtureEncoder) SupportsDistributedRequest(core.HLSRequest) bool { return true }

func testPreparationFailures(t *testing.T, ctx context.Context, front, source *Runtime, owner, controller nodewire.Identity) {
	t.Helper()
	remote, err := NewRemotePlayback(front, RemotePlaybackOptions{Estimate: func(context.Context, *core.Title, core.ClientCaps) core.MediaInfo { return core.MediaInfo{} }, SelectSource: func(context.Context) (nodewire.Identity, error) { return owner, nil }, Capacity: 32})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		viewer int64
		exact  bool
		client string
	}{{"denied-before-acquire", 777, false, "denied-client"}, {"invalid-exact-before-acquire", 9, true, "invalid-client"}, {"error-with-life", 9, false, "owned-error-client"}} {
		_, life, err := remote.PrepareSurfaceSource(ctx, &core.User{ID: test.viewer}, core.MovieKey(1), core.ClientCaps{ClientID: test.client}, "", test.exact)
		if err == nil || life == nil {
			t.Fatalf("%s did not retain error lifetime: %v", test.name, err)
		}
		select {
		case <-life.Done():
			t.Fatal("failure alone proved retirement")
		default:
		}
		for i := 0; i < 50; i++ {
			_ = front.Tick(ctx)
			source.Cleanup()
			select {
			case <-life.Done():
				if life.Err() != nil {
					t.Fatal(life.Err())
				}
				goto settled
			default:
				time.Sleep(time.Millisecond)
			}
		}
		t.Fatalf("%s failed to settle exact operation", test.name)
	settled:
	}
	// Retirement of an unknown request seals the operation before a delayed
	// original request can acquire a source; no Prepare replay is issued.
	op := "retire-before-delayed-prepare"
	if retired, err := front.RetirePreparedSource(ctx, owner, op); err != nil || !retired {
		t.Fatal("unknown operation was not sealed", err)
	}
	if _, err := front.PrepareSource(ctx, owner, PrepareRequest{Operation: op, Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: "must-not-open"}, Protocol: "hls"}); err == nil {
		t.Fatal("delayed prepare bypassed retirement tombstone")
	}
	// Simulate successful source acquisition with a lost response. The frontend
	// retains only the immutable operation; Tick must retire it without reprepare.
	request := PrepareRequest{Operation: "lost-prepare-result", Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: "lost-result-client"}, Protocol: "hls"}
	if _, err := front.PrepareSource(ctx, owner, request); err != nil {
		t.Fatal(err)
	}
	entry := &remotePreparation{operation: request.Operation, owner: owner, request: request, retiring: true, done: make(chan struct{})}
	remote.mu.Lock()
	remote.entries["lost-result"] = entry
	remote.mu.Unlock()
	source.mu.Lock()
	count := len(source.preparations)
	source.mu.Unlock()
	for i := 0; i < 50; i++ {
		_ = front.Tick(ctx)
		source.Cleanup()
		select {
		case <-entry.done:
			source.mu.Lock()
			same := len(source.preparations) == count
			source.mu.Unlock()
			if !same {
				t.Fatal("lost-result reconciliation created another operation")
			}
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatal("lost-result operation did not retire")
}
