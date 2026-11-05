package nodesource

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// ClientOptions is trusted composition, never a public request payload. Endpoint
// is one fixed HTTPS origin with Path. TLS performs ordinary Go chain/hostname
// verification BEFORE BindServer. Peer is this client's certificate boot identity.
// Callers must not mutate TLS credentials/callbacks after construction.
type ClientOptions struct {
	Endpoint    string
	TLS         *tls.Config
	BindServer  func(tls.ConnectionState) error
	Peer        Peer
	MaxLifetime time.Duration
	Timeout     time.Duration
	Now         func() time.Time
}
type Client struct {
	o         ClientOptions
	http      *http.Client
	transport *http.Transport
}

func NewClient(o ClientOptions) (*Client, error) {
	u, e := url.Parse(o.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.Path != Path || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" || u.ForceQuery || o.TLS == nil || o.TLS.InsecureSkipVerify || o.TLS.RootCAs == nil || (len(o.TLS.Certificates) == 0 && o.TLS.GetClientCertificate == nil) || o.BindServer == nil || o.Now == nil || o.Peer.NodeID == "" || o.Peer.Boot == "" || o.MaxLifetime <= 0 || o.MaxLifetime > time.Hour || o.Timeout <= 0 || o.Timeout > time.Hour {
		return nil, errors.New("nodesource: invalid client dependencies")
	}
	tc := o.TLS.Clone()
	tc.RootCAs = tc.RootCAs.Clone()
	if tc.MinVersion < tls.VersionTLS12 {
		tc.MinVersion = tls.VersionTLS12
	}
	previous := tc.VerifyConnection
	tc.VerifyConnection = func(s tls.ConnectionState) error {
		if len(s.VerifiedChains) == 0 {
			return errors.New("nodesource: unverified server")
		}
		if previous != nil {
			if e := previous(s); e != nil {
				return e
			}
		}
		return o.BindServer(s)
	}
	tr := &http.Transport{TLSClientConfig: tc, Proxy: nil, DialContext: (&net.Dialer{Timeout: o.Timeout}).DialContext, MaxIdleConns: 4, MaxIdleConnsPerHost: 4, MaxConnsPerHost: 16, IdleConnTimeout: 30 * time.Second, ResponseHeaderTimeout: o.Timeout, DisableCompression: true}
	return &Client{o: o, transport: tr, http: &http.Client{Transport: tr, Timeout: o.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Close() { c.transport.CloseIdleConnections() }
func (c *Client) valid(g Grant) bool {
	return validGrant(g, c.o.Now(), c.o.MaxLifetime) && c.o.Peer == (Peer{g.Scope.Fence.Target.NodeID, g.Scope.Fence.Target.Boot})
}
func (c *Client) request(ctx context.Context, g Grant, method, ran string) (*http.Response, error) {
	if !c.valid(g) {
		return nil, errors.New("nodesource: invalid grant")
	}
	req, e := http.NewRequestWithContext(ctx, method, c.o.Endpoint, nil)
	if e != nil {
		return nil, e
	}
	req.Header.Set(CredentialHeader, g.Credential)
	if ran != "" {
		req.Header.Set("Range", ran)
	}
	res, e := c.http.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("nodesource: transport failure")
	}
	return res, nil
}
func (c *Client) head(ctx context.Context, g Grant) (int64, error) {
	res, e := c.request(ctx, g, http.MethodHead, "")
	if e != nil {
		return 0, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || res.ContentLength < 0 || res.Header.Get("ETag") != etag(g.Scope) || res.Header.Get("Content-Range") != "" || res.Header.Get("Content-Encoding") != "" || strings.HasPrefix(res.Header.Get("Content-Type"), "multipart/") {
		return 0, errors.New("nodesource: invalid HEAD representation")
	}
	return res.ContentLength, nil
}

// Open pins the exact immutable revision, ETag and size via an authorized HEAD.
func (c *Client) Open(ctx context.Context, g Grant) (*Reader, error) {
	size, e := c.head(ctx, g)
	if e != nil {
		return nil, e
	}
	life, cancel := context.WithCancel(context.Background())
	return &Reader{client: c, grant: g, size: size, ctx: life, cancel: cancel, done: make(chan struct{})}, nil
}

// Reader is safe for concurrent ReaderAt calls. Close cancels local network work.
// Done proves only local HTTP requests exited; neither Close nor EOF nor Done
// attests remote producer, source or encoder retirement. Host lifecycle must
// reconcile source-node holds independently, even after grant expiry.
type Reader struct {
	client *Client
	mu     sync.Mutex
	grant  Grant
	size   int64
	ctx    context.Context
	cancel context.CancelFunc
	closed bool
	active int
	done   chan struct{}
}

func (r *Reader) Size() int64           { return r.size }
func (r *Reader) Name() string          { return "source" }
func (r *Reader) ReleaseID() string     { r.mu.Lock(); defer r.mu.Unlock(); return r.grant.Scope.Revision }
func (r *Reader) Done() <-chan struct{} { return r.done }
func (r *Reader) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.closed {
		r.closed = true
		r.cancel()
		if r.active == 0 {
			close(r.done)
		}
	}
	return nil
}
func (r *Reader) begin(ctx context.Context) (context.Context, Grant, func(), error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, Grant{}, nil, errors.New("nodesource: closed")
	}
	r.active++
	combined, cancel := context.WithDeadline(ctx, r.grant.Expires)
	stop := context.AfterFunc(r.ctx, cancel)
	g := r.grant
	return combined, g, func() {
		stop()
		cancel()
		r.mu.Lock()
		defer r.mu.Unlock()
		r.active--
		if r.closed && r.active == 0 {
			close(r.done)
		}
	}, nil
}
func (r *Reader) ReadAt(b []byte, off int64) (int, error) {
	return r.ReadAtContext(context.Background(), b, off)
}
func (r *Reader) ReadAtContext(ctx context.Context, b []byte, off int64) (int, error) {
	ctx, g, end, e := r.begin(ctx)
	if e != nil {
		return 0, e
	}
	defer end()
	// This tracker accounts for LOCAL network producers only; never forward it as
	// evidence of remote source retirement.
	if tracker := core.ReadRetirementFrom(ctx); tracker != nil {
		release, ok := tracker.Begin()
		if !ok {
			return 0, errors.New("nodesource: sealed read")
		}
		defer release()
	}
	if e := ctx.Err(); e != nil {
		return 0, e
	}
	if off < 0 {
		return 0, errors.New("nodesource: negative offset")
	}
	if len(b) == 0 {
		return 0, nil
	}
	if off >= r.size {
		return 0, io.EOF
	}
	want := int64(len(b))
	if want > r.size-off {
		want = r.size - off
	}
	res, e := r.client.request(ctx, g, http.MethodGet, fmt.Sprintf("bytes=%d-%d", off, off+want-1))
	if e != nil {
		return 0, e
	}
	defer res.Body.Close()
	expected := fmt.Sprintf("bytes %d-%d/%d", off, off+want-1, r.size)
	if res.StatusCode != 206 || res.Header.Get("Content-Range") != expected || res.ContentLength != want || res.Header.Get("ETag") != etag(g.Scope) || res.Header.Get("Content-Encoding") != "" || strings.HasPrefix(res.Header.Get("Content-Type"), "multipart/") {
		return 0, errors.New("nodesource: invalid range representation")
	}
	n, e := io.ReadFull(res.Body, b[:want])
	if e != nil {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		return n, io.ErrUnexpectedEOF
	}
	var extra [1]byte
	if nExtra, tailErr := res.Body.Read(extra[:]); nExtra != 0 || tailErr != io.EOF {
		return n, errors.New("nodesource: invalid response length")
	}
	if want < int64(len(b)) {
		return n, io.EOF
	}
	return n, nil
}

// Renew obtains fresh authority rather than extending a raw deadline. Every
// Scope field and the HEAD representation must remain identical. Existing reads
// retain their old deadline. Renewal cannot revive an expired grant.
func (r *Reader) Renew(ctx context.Context, authority func(context.Context, Grant) (Grant, error)) error {
	if authority == nil {
		return errors.New("nodesource: no renewal authority")
	}
	ctx, old, end, e := r.begin(ctx)
	if e != nil {
		return e
	}
	defer end()
	if !r.client.valid(old) {
		return errors.New("nodesource: expired grant")
	}
	next, e := authority(ctx, old)
	if e != nil {
		return e
	}
	if next.Scope != old.Scope || !r.client.valid(next) || !next.Expires.After(old.Expires) || next.Credential == old.Credential {
		return errors.New("nodesource: invalid renewal")
	}
	size, e := r.client.head(ctx, next)
	if e != nil {
		return e
	}
	if size != r.size {
		return errors.New("nodesource: changed size")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed || r.grant != old || !r.client.valid(old) {
		return errors.New("nodesource: stale renewal")
	}
	r.grant = next
	return nil
}
