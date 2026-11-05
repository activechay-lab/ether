package remotetranscode

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestMutualTLSAdapterAndDisconnect(t *testing.T) {
	w, f, start := fixture(t)
	w.opts.ValidateRenewal = receipt
	caps := Supported()
	caps.ABR, caps.DASH, caps.HTTP = true, true, true
	caps.SoftSubtitles, caps.Controls = true, true
	w.opts.Capabilities = caps
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "controller-boot"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pair := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	// Use the same fixture identity for client/server keys in this offline test.
	// Production provisions distinct boot-bound identities and authorizes placement.
	server := httptest.NewUnstartedServer(w)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{pair}, VerifyConnection: func(c tls.ConnectionState) error {
		if !c.PeerCertificates[0].Equal(cert) {
			return errors.New("wrong worker")
		}
		return nil
	}}}}
	a, err := NewAdapter(AdapterOptions{Capabilities: caps, Endpoint: server.URL, Client: client, RecheckServer: func(_ context.Context, s tls.ConnectionState) error {
		return client.Transport.(*http.Transport).TLSClientConfig.VerifyConnection(s)
	}, Capacity: 4, Assign: func(_ context.Context, req core.HLSRequest) (Assignment, error) {
		id := start.Identity
		id.Session = req.SessionID
		return Assignment{Identity: id, Grant: start.Grant, Expires: start.Expires}, nil
	}, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision { return start.Decision }})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	hs, proof, err := a.StartHLSOwned(context.Background(), core.HLSRequest{SessionID: start.Identity.Session, InputURL: "http://127.0.0.1/must-never-be-forwarded", Decision: start.Decision})
	if err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	hs.ServeSegment(out, httptest.NewRequest("GET", "https://frontend/segment", nil), -1)
	if out.Code != 206 || out.Body.String() != "abc" || out.Header().Get("Content-Range") != "bytes 0-2/3" {
		t.Fatalf("proxy lost representation: %v", out)
	}
	remote := hs.(*Session)
	n := renewal(start)
	lease, err := remote.Renew(context.Background(), n)
	if err != nil || lease.Version != 1 || !sameRenewal(*lease.Accepted, n) {
		t.Fatalf("renew over mTLS: %v %+v", err, lease)
	}
	// Repeating the exact operation cannot add authority.
	lease, err = remote.Renew(context.Background(), n)
	if err != nil || !lease.Deadline.Equal(n.Deadline) {
		t.Fatal("retry extended lease", err)
	}
	for _, protocol := range []string{core.ProtocolHLS, core.ProtocolDASH, core.ProtocolHTTP} {
		req := core.HLSRequest{SessionID: "protocol-" + protocol, Protocol: protocol, Decision: start.Decision}
		if protocol == core.ProtocolHLS {
			index := 2
			req.SoftSubtitleStream = &index
			req.Decision.SubtitleStream = index
			req.Info.Streams = []core.StreamInfo{{Index: 2, Type: "subtitle", Codec: "subrip"}}
		}
		next, _, err := a.StartHLSOwned(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		checkRepresentations(t, next, protocol)
		if protocol == core.ProtocolHLS {
			out := httptest.NewRecorder()
			if !next.(*Session).ServeSoftSubtitle(out, httptest.NewRequest("GET", "https://frontend/subtitle", nil), 2) || out.Body.String() != "[Script Info]\n[Events]\n" {
				t.Fatal("lost embedded ASS output")
			}
			if next.(*Session).ServeSoftSubtitle(httptest.NewRecorder(), httptest.NewRequest("GET", "https://frontend/subtitle", nil), 3) {
				t.Fatal("unselected subtitle admitted")
			}
		}
		next.(*Session).Touch()
		if !f.session.touched {
			t.Fatal("touch did not reach worker")
		}
		if protocol != core.ProtocolHTTP {
			next.(*Session).PlaybackState("paused")
			if f.session.playback != "paused" {
				t.Fatal("pause did not reach worker")
			}
		}
		if err := next.Close(); err != core.ErrEncoderPending {
			t.Fatalf("lost retirement charge: %v", err)
		}
	}
	server.Close()
	if err := hs.Close(); !errors.Is(err, ErrUncertain) {
		t.Fatalf("disconnect reported retirement: %v", err)
	}
	if proof.EncoderErr() != core.ErrEncoderPending {
		t.Fatal("disconnect manufactured proof")
	}
}

func TestExpiryAndCapacityRetainResponsibility(t *testing.T) {
	w, f, s := fixture(t)
	s.Expires = time.Now().Add(40 * time.Millisecond)
	if state(t, rpc(w, "start", s, true)) != "running" {
		t.Fatal("start")
	}
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		w.mu.Lock()
		stopped := f.session.stopped
		w.mu.Unlock()
		if stopped {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("expiry did not stop")
		}
	}
	if state(t, rpc(w, "status", s, true)) != "pending" {
		t.Fatal("expiry proved retirement")
	}
	w.mu.Lock()
	w.opts.Capacity = 1
	w.mu.Unlock()
	next := s
	next.Identity.Session = "other"
	next.Expires = time.Now().Add(30 * time.Second)
	if rpc(w, "start", next, true).Code != 409 {
		t.Fatal("evicted pending execution")
	}
	if len(w.records) != 1 {
		t.Fatal("unbounded registry")
	}
}
