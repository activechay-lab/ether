// Package outbound provides the private per-operation TLS admission boundary.
package outbound

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// ErrEndpointUnavailable means an explicit refusal, absent DNS name, or no
// network/host route. It allows replacement, never resource retirement.
var ErrEndpointUnavailable = errors.New("outbound: endpoint unavailable")

// ErrPeerDenied identifies explicit TLS identity/policy refusal, not a timeout
// or connection failure. Callers may replace work but cannot infer retirement.
var ErrPeerDenied = errors.New("outbound: peer denied")

type operationContext struct{}

// Transport admits one fresh, fully verified HTTP/1.1 TLS connection per
// operation. There is deliberately no connection reuse or session resumption.
// Rebuild after credential/root rotation. Existing transports still call the
// current policy on every operation. Policy and TLS callbacks must cooperate
// with cancellation and be concurrency safe; opaque signer keys and callback
// captures must remain immutable. No locks are held across callbacks/network IO.
type Transport struct {
	http   *http.Transport
	origin string
}

// New ignores caller HTTP dial/proxy hooks entirely. check must compose current
// nodewire.Guard.RecheckServer with the expected complete identity and hostname.
// timeout bounds TCP, TLS and cooperative policy admission, not response bodies.
func New(endpoint string, config *tls.Config, timeout time.Duration, check func(context.Context, tls.ConnectionState) error) (*Transport, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || u.ForceQuery || config == nil || config.InsecureSkipVerify || config.RootCAs == nil || check == nil || timeout <= 0 || timeout > time.Hour || (len(config.Certificates) == 0 && config.GetClientCertificate == nil) {
		return nil, errors.New("outbound: invalid dependencies")
	}
	tc := config.Clone()
	if verify := tc.VerifyConnection; verify != nil {
		tc.VerifyConnection = func(state tls.ConnectionState) error {
			if err := verify(state); err != nil {
				return ErrPeerDenied
			}
			return nil
		}
	}
	tc.RootCAs = config.RootCAs.Clone()
	tc.NextProtos = []string{"http/1.1"}
	tc.SessionTicketsDisabled = true
	tc.ClientSessionCache = nil
	tc.CipherSuites = append([]uint16(nil), config.CipherSuites...)
	tc.CurvePreferences = append([]tls.CurveID(nil), config.CurvePreferences...)
	if tc.MinVersion < tls.VersionTLS12 {
		tc.MinVersion = tls.VersionTLS12
	}
	tc.Certificates = make([]tls.Certificate, len(config.Certificates))
	for i, c := range config.Certificates {
		c.Certificate = cloneBytes(c.Certificate)
		c.OCSPStaple = append([]byte(nil), c.OCSPStaple...)
		c.SignedCertificateTimestamps = cloneBytes(c.SignedCertificateTimestamps)
		c.SupportedSignatureAlgorithms = append([]tls.SignatureScheme(nil), c.SupportedSignatureAlgorithms...)
		c.Leaf = nil
		if len(c.Certificate) > 0 {
			c.Leaf, err = x509.ParseCertificate(c.Certificate[0])
			if err != nil {
				return nil, errors.New("outbound: invalid certificate")
			}
		}
		// Standard software keys are copied; opaque crypto.Signer implementations
		// remain caller-owned immutable dependencies, like TLS callbacks.
		if der, e := x509.MarshalPKCS8PrivateKey(c.PrivateKey); e == nil {
			c.PrivateKey, err = x509.ParsePKCS8PrivateKey(der)
			if err != nil {
				return nil, errors.New("outbound: invalid key")
			}
		}
		tc.Certificates[i] = c
	}
	// Never retain a caller-owned certificate map. Client selection uses the
	// copied Certificates or the explicitly trusted GetClientCertificate callback.
	tc.NameToCertificate = nil
	if tc.ServerName == "" {
		tc.ServerName = u.Hostname()
	}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true, DisableCompression: true, MaxConnsPerHost: 16, MaxIdleConns: 1, MaxIdleConnsPerHost: 1, ResponseHeaderTimeout: timeout, TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
	tr.DialTLSContext = func(dialCtx context.Context, network, addr string) (net.Conn, error) {
		// Go Transport.getConn uses WithoutCancel but retains context values. Restore
		// the operation's context; fail closed if a future transport drops the value.
		original, ok := dialCtx.Value(operationContext{}).(context.Context)
		if !ok {
			return nil, errors.New("outbound: missing operation context")
		}
		ctx, cancel := context.WithTimeout(original, timeout)
		defer cancel()
		raw, e := (&net.Dialer{}).DialContext(ctx, network, addr)
		if e != nil {
			var dns *net.DNSError
			if ctx.Err() == nil && (errors.Is(e, syscall.ECONNREFUSED) || errors.Is(e, syscall.ENETUNREACH) || errors.Is(e, syscall.EHOSTUNREACH) || errors.As(e, &dns) && dns.IsNotFound) {
				return nil, ErrEndpointUnavailable
			}
			return nil, errors.New("outbound: connection denied")
		}
		conn := tls.Client(raw, tc)
		// HTTP never receives this connection until BOTH ordinary TLS verification
		// (including original callbacks) and current operation policy have succeeded.
		if e = conn.HandshakeContext(ctx); e == nil {
			state := conn.ConnectionState()
			if !state.HandshakeComplete || len(state.VerifiedChains) == 0 || (state.NegotiatedProtocol != "" && state.NegotiatedProtocol != "http/1.1") {
				e = errors.New("outbound: unverified peer")
			} else {
				e = check(ctx, state)
				if e != nil && ctx.Err() == nil {
					e = ErrPeerDenied
				}
			}
		}
		if e == nil {
			e = ctx.Err()
		}
		if e != nil {
			conn.Close()
			var certificateError *tls.CertificateVerificationError
			if ctx.Err() == nil && (errors.Is(e, ErrPeerDenied) || errors.As(e, &certificateError)) {
				return nil, ErrPeerDenied
			}
			return nil, errors.New("outbound: connection denied")
		}
		return conn, nil
	}
	return &Transport{http: tr, origin: u.Scheme + "://" + u.Host}, nil
}
func cloneBytes(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i := range in {
		out[i] = append([]byte(nil), in[i]...)
	}
	return out
}
func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r == nil {
		return nil, errors.New("outbound: invalid request")
	}
	if r.URL == nil || r.URL.Scheme+"://"+r.URL.Host != t.origin || r.URL.User != nil || (r.Host != "" && r.Host != r.URL.Host) {
		if r.Body != nil {
			r.Body.Close()
		}
		return nil, errors.New("outbound: invalid origin")
	}
	r = r.WithContext(context.WithValue(r.Context(), operationContext{}, r.Context()))
	return t.http.RoundTrip(r)
}
func (t *Transport) CloseIdleConnections() { t.http.CloseIdleConnections() }

// ResponseHeaderTimeout sets the wait for the first response headers separately
// from TCP/TLS admission. Call only before the transport's first request.
// Media may wait for an encoder segment while dead endpoints must fail quickly.
func (t *Transport) ResponseHeaderTimeout(timeout time.Duration) {
	t.http.ResponseHeaderTimeout = timeout
}
