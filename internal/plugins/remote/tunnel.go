package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Tunnel wire protocol (v1). Every frame is one JSON text message.
// Bodies travel base64-encoded so the same protocol is trivial in the
// Python and TypeScript reference clients. Frame shapes:
//
//	hello    client->server  {type, protocol, plugin?, features?}
//	welcome  server->client  {type, protocol, pluginId, connectionId, maxChunkBytes}
//	request  server->client  {type, id, requestId, method, path, query, headers, body?, deadline?}
//	response client->server  {type, id, status, headers, body?}          (buffered reply)
//	response_start/_chunk/_end/_error client->server                      (streamed reply)
//	cancel   server->client  {type, id}     (the call's deadline passed)
//	error    either          {type, id?, error:{code,message}}
//
// Websocket ping/pong carries liveness. A sidecar handles requests
// concurrently; ids correlate replies, so completion order is free.

// Default tunnel limits.
const (
	DefaultMaxInFlight   = 32
	DefaultChunkBytes    = 64 << 10
	DefaultPingInterval  = 20 * time.Second
	defaultWriteTimeout  = 10 * time.Second
	defaultReadLimit     = 4 << 20 // one frame; a 64 KiB chunk base64s well under this
	defaultHelloTimeout  = 10 * time.Second
	streamChunkQueueSize = 8
)

type frame struct {
	Type      string              `json:"type"`
	ID        string              `json:"id,omitempty"`
	RequestID string              `json:"requestId,omitempty"`
	Protocol  string              `json:"protocol,omitempty"`
	Plugin    string              `json:"plugin,omitempty"`
	PluginID  string              `json:"pluginId,omitempty"`
	ConnID    string              `json:"connectionId,omitempty"`
	Features  []string            `json:"features,omitempty"`
	MaxChunk  int                 `json:"maxChunkBytes,omitempty"`
	Method    string              `json:"method,omitempty"`
	Path      string              `json:"path,omitempty"`
	Query     map[string][]string `json:"query,omitempty"`
	Headers   map[string][]string `json:"headers,omitempty"`
	Body      string              `json:"body,omitempty"` // base64
	Data      string              `json:"data,omitempty"` // base64 chunk
	Deadline  string              `json:"deadline,omitempty"`
	Status    int                 `json:"status,omitempty"`
	Error     *frameError         `json:"error,omitempty"`
}

type frameError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *frameError) error() error {
	if e == nil {
		return errors.New("remote: plugin error")
	}
	if e.Code == "deadline" {
		return fmt.Errorf("%w: %s", context.DeadlineExceeded, e.Message)
	}
	return fmt.Errorf("remote: plugin error %s: %s", e.Code, e.Message)
}

func encodeBody(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(b)
}

func decodeBody(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(s)
}

// TunnelServer accepts sidecar tunnel connections and hands out a
// Runtime per connected plugin. One live connection per plugin: a
// reconnect replaces the old socket, whose in-flight calls fail with
// ErrDisconnected. Runtimes obtained from RuntimeFor resolve the
// current connection at call time, so they survive reconnects.
type TunnelServer struct {
	onConnect    func(pluginID string)
	auth         CredentialResolver
	locale       string
	maxBytes     int64
	maxInFlight  int
	chunkBytes   int
	pingInterval time.Duration
	timeout      time.Duration
	log          *slog.Logger

	mu     sync.Mutex
	conns  map[string]*tunnelConn
	leases map[string]int
	closed bool
}

// TunnelOptions configures a TunnelServer.
type TunnelOptions struct {
	// Auth resolves tunnel credentials to plugin ids. Required: a
	// server without it refuses every connection.
	Auth CredentialResolver
	// Locale is sent to plugins unless a request overrides it.
	Locale string
	// MaxResponseBytes caps buffered replies; zero uses the default.
	MaxResponseBytes int64
	// MaxInFlight bounds concurrent calls per connection
	// (backpressure); zero uses DefaultMaxInFlight.
	MaxInFlight int
	// ChunkBytes is the streamed-body chunk size advertised to
	// sidecars; zero uses DefaultChunkBytes.
	ChunkBytes int
	// PingInterval is the websocket keepalive cadence; zero uses
	// DefaultPingInterval, negative disables server pings.
	PingInterval time.Duration
	// Timeout is the default per-call deadline; zero uses
	// DefaultCallTimeout.
	Timeout time.Duration
	Logger  *slog.Logger
}

// SetOnConnect installs a hook run (in its own goroutine) each time a
// sidecar connects, so the host can refresh the plugin's health at once
// instead of showing the failures recorded while it was away.
func (s *TunnelServer) SetOnConnect(f func(pluginID string)) {
	s.mu.Lock()
	s.onConnect = f
	s.mu.Unlock()
}

func (s *TunnelServer) connectHook() func(string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.onConnect
}

// NewTunnelServer builds a tunnel endpoint. Mount Handler at
// TunnelPath.
func NewTunnelServer(opts TunnelOptions) *TunnelServer {
	s := &TunnelServer{
		auth:         opts.Auth,
		locale:       opts.Locale,
		maxBytes:     opts.MaxResponseBytes,
		maxInFlight:  opts.MaxInFlight,
		chunkBytes:   opts.ChunkBytes,
		pingInterval: opts.PingInterval,
		timeout:      opts.Timeout,
		log:          opts.Logger,
		conns:        map[string]*tunnelConn{},
		leases:       map[string]int{},
	}
	if s.maxBytes <= 0 {
		s.maxBytes = DefaultMaxResponseBytes
	}
	if s.maxInFlight <= 0 {
		s.maxInFlight = DefaultMaxInFlight
	}
	if s.chunkBytes <= 0 {
		s.chunkBytes = DefaultChunkBytes
	}
	if s.pingInterval == 0 {
		s.pingInterval = DefaultPingInterval
	}
	if s.timeout <= 0 {
		s.timeout = DefaultCallTimeout
	}
	if s.log == nil {
		s.log = slog.Default()
	}
	return s
}

// Handler serves GET TunnelPath: authenticate, upgrade, then serve
// the connection until it ends. Authentication happens before the
// upgrade so a bad credential gets a plain 401, never a socket.
func (s *TunnelServer) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeHostError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET to open a tunnel")
			return
		}
		if s.auth == nil {
			writeHostError(w, http.StatusServiceUnavailable, "unavailable", "Plugin tunnel is not configured")
			return
		}
		cred := credentialFromRequest(r)
		pluginID, err := s.auth.ResolvePluginCredential(r.Context(), cred)
		if err != nil || pluginID == "" {
			writeHostError(w, http.StatusUnauthorized, "unauthorized", "Plugin credential rejected")
			return
		}
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{TunnelProtocol},
		})
		if err != nil {
			s.log.Debug("plugin tunnel accept failed", "plugin", pluginID, "err", err)
			return
		}
		ws.SetReadLimit(defaultReadLimit)
		s.serve(r.Context(), ws, pluginID, cred)
	})
}

// serve runs one accepted connection: hello, welcome, then the read
// and ping loops until either side goes away.
func (s *TunnelServer) serve(ctx context.Context, ws *websocket.Conn, pluginID, credential string) {
	ctx, cancel := context.WithCancel(ctx)
	conn := newTunnelConn(s, ws, pluginID, cancel)
	conn.credential = credential
	defer conn.teardown(ErrDisconnected)

	helloCtx, helloCancel := context.WithTimeout(ctx, defaultHelloTimeout)
	_, raw, err := ws.Read(helloCtx)
	helloCancel()
	if err != nil {
		ws.Close(websocket.StatusProtocolError, "hello required")
		return
	}
	var hello frame
	if json.Unmarshal(raw, &hello) != nil || hello.Type != "hello" {
		ws.Close(websocket.StatusProtocolError, "hello required")
		return
	}
	if hello.Protocol != "" && hello.Protocol != TunnelProtocol {
		ws.Close(websocket.StatusProtocolError, "unknown tunnel protocol")
		return
	}
	if hello.Plugin != "" && hello.Plugin != pluginID {
		ws.Close(websocket.StatusProtocolError, "plugin id does not match credential")
		return
	}

	// Welcome before the connection is published: once it is in
	// s.conns a call can write a request frame on it, and a request
	// arriving ahead of the welcome fails the sidecar's handshake.
	if err := conn.write(ctx, frame{
		Type:     "welcome",
		Protocol: TunnelProtocol,
		PluginID: pluginID,
		ConnID:   conn.id,
		MaxChunk: s.chunkBytes,
	}); err != nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		ws.Close(websocket.StatusGoingAway, "server closing")
		return
	}
	old := s.conns[pluginID]
	s.conns[pluginID] = conn
	s.mu.Unlock()
	if old != nil {
		// A reconnect replaces the old socket. Its in-flight calls
		// fail cleanly instead of hanging to their deadlines.
		old.teardown(ErrDisconnected)
	}
	s.log.Info("plugin tunnel connected", "plugin", pluginID, "connection", conn.id)
	if f := s.connectHook(); f != nil {
		go f(pluginID)
	}

	var wg sync.WaitGroup
	if s.pingInterval > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.NewTicker(s.pingInterval)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					pingCtx, pingCancel := context.WithTimeout(ctx, defaultWriteTimeout)
					err := ws.Ping(pingCtx)
					pingCancel()
					if err != nil {
						cancel()
						return
					}
				}
			}
		}()
	}
	conn.readLoop(ctx)
	cancel()
	wg.Wait()

	s.mu.Lock()
	if s.conns[pluginID] == conn {
		delete(s.conns, pluginID)
	}
	s.mu.Unlock()
	s.log.Info("plugin tunnel disconnected", "plugin", pluginID, "connection", conn.id)
}

// RuntimeFor returns the runtime for a plugin. The runtime resolves
// the plugin's current connection per call, so it keeps working
// across sidecar reconnects; calls with no sidecar connected fail
// with ErrDisconnected.
func (s *TunnelServer) RuntimeFor(pluginID string) Runtime {
	s.mu.Lock()
	s.leases[pluginID]++
	s.mu.Unlock()
	return &tunnelRuntime{server: s, pluginID: pluginID}
}

// Disconnect drops a plugin's current socket, including in-flight requests.
func (s *TunnelServer) Disconnect(pluginID string) {
	s.mu.Lock()
	conn := s.conns[pluginID]
	s.mu.Unlock()
	if conn != nil {
		conn.teardown(ErrClosed)
	}
}

// Connected lists the plugin ids with a live tunnel.
func (s *TunnelServer) Connected() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.conns))
	for id := range s.conns {
		out = append(out, id)
	}
	return out
}

// IsConnected reports whether a plugin's sidecar is connected.
func (s *TunnelServer) IsConnected(pluginID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.conns[pluginID]
	return ok
}

func (s *TunnelServer) connFor(pluginID string) (*tunnelConn, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	c, ok := s.conns[pluginID]
	if !ok {
		return nil, fmt.Errorf("%w: plugin %q has no sidecar connected", ErrDisconnected, pluginID)
	}
	return c, nil
}

// Close drops every tunnel connection. In-flight calls fail.
func (s *TunnelServer) Close() error {
	s.mu.Lock()
	s.closed = true
	conns := s.conns
	s.conns = map[string]*tunnelConn{}
	s.mu.Unlock()
	for _, c := range conns {
		c.teardown(ErrClosed)
	}
	return nil
}

// tunnelRuntime is the per-plugin Runtime over a TunnelServer.
type tunnelRuntime struct {
	server   *TunnelServer
	pluginID string
	// Credential, when set, is echoed to the plugin in the call
	// headers for symmetry with URL mode. The tunnel itself is
	// already authenticated, so WS7 normally leaves this empty.
	Credential string
	closeOnce  sync.Once
}

// PluginID returns the plugin this runtime calls.
func (r *tunnelRuntime) PluginID() string { return r.pluginID }

func (r *tunnelRuntime) Call(ctx context.Context, req Request) (Response, error) {
	conn, err := r.server.connFor(r.pluginID)
	if err != nil {
		return Response{}, err
	}
	if err := conn.authorize(ctx); err != nil {
		return Response{}, err
	}
	return conn.call(ctx, r.prepare(req), r.Credential)
}

func (r *tunnelRuntime) CallStream(ctx context.Context, req Request) (*StreamResponse, error) {
	conn, err := r.server.connFor(r.pluginID)
	if err != nil {
		return nil, err
	}
	if err := conn.authorize(ctx); err != nil {
		return nil, err
	}
	return conn.callStream(ctx, r.prepare(req), r.Credential)
}

// Close drops this plugin's tunnel connection, if any. The sidecar
// may reconnect; use TunnelServer.Close to stop the endpoint.
func (r *tunnelRuntime) Close() error {
	r.closeOnce.Do(func() {
		r.server.mu.Lock()
		r.server.leases[r.pluginID]--
		var conn *tunnelConn
		if r.server.leases[r.pluginID] <= 0 {
			delete(r.server.leases, r.pluginID)
			conn = r.server.conns[r.pluginID]
		}
		r.server.mu.Unlock()
		if conn != nil {
			conn.teardown(ErrClosed)
		}
	})
	return nil
}

func (r *tunnelRuntime) prepare(req Request) Request {
	if req.Locale == "" {
		req.Locale = r.server.locale
	}
	return req
}

var _ Runtime = (*tunnelRuntime)(nil)
var _ Streamer = (*tunnelRuntime)(nil)

// pendingCall tracks one in-flight request on a connection. Exactly
// one of the channels delivers: resp for a buffered reply, or start
// then chunks then a terminal err on done for a streamed reply. A
// buffered reply also arrives through the stream path internally:
// Call reads the stream to the cap, so both reply styles share one
// state machine.
type pendingCall struct {
	start   chan startInfo
	chunks  chan []byte
	done    chan error // buffered 1: nil on clean end
	ctxDone <-chan struct{}
}

type startInfo struct {
	status  int
	headers map[string][]string
	err     error
}

type tunnelConn struct {
	server     *TunnelServer
	ws         *websocket.Conn
	pluginID   string
	credential string
	id         string
	cancel     context.CancelFunc

	writeMu sync.Mutex
	sem     chan struct{}

	mu      sync.Mutex
	pending map[string]*pendingCall
	nextID  uint64
	dead    error
	once    sync.Once
}

func newTunnelConn(s *TunnelServer, ws *websocket.Conn, pluginID string, cancel context.CancelFunc) *tunnelConn {
	return &tunnelConn{
		server:   s,
		ws:       ws,
		pluginID: pluginID,
		id:       newRequestID()[:12],
		cancel:   cancel,
		sem:      make(chan struct{}, s.maxInFlight),
		pending:  map[string]*pendingCall{},
	}
}

// Credentials can expire or be revoked while a socket stays open.
func (c *tunnelConn) authorize(ctx context.Context) error {
	id, err := c.server.auth.ResolvePluginCredential(ctx, c.credential)
	if err != nil || id != c.pluginID {
		c.teardown(ErrUnauthorized)
		return ErrUnauthorized
	}
	return nil
}

func (c *tunnelConn) write(ctx context.Context, f frame) error {
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, defaultWriteTimeout)
	defer cancel()
	return c.ws.Write(wctx, websocket.MessageText, raw)
}

// teardown fails every in-flight call and closes the socket. Safe to
// call more than once; the first reason wins.
func (c *tunnelConn) teardown(reason error) {
	c.once.Do(func() {
		c.mu.Lock()
		c.dead = reason
		pending := c.pending
		c.pending = map[string]*pendingCall{}
		c.mu.Unlock()
		for _, p := range pending {
			select {
			case p.start <- startInfo{err: reason}:
			default:
			}
			select {
			case p.done <- reason:
			default:
			}
		}
		c.cancel()
		c.ws.Close(websocket.StatusGoingAway, "connection closed")
	})
}

func (c *tunnelConn) readLoop(ctx context.Context) {
	for {
		_, raw, err := c.ws.Read(ctx)
		if err != nil {
			c.teardown(ErrDisconnected)
			return
		}
		var f frame
		if json.Unmarshal(raw, &f) != nil {
			c.server.log.Debug("plugin tunnel bad frame", "plugin", c.pluginID)
			continue
		}
		c.dispatch(ctx, f)
	}
}

func (c *tunnelConn) dispatch(ctx context.Context, f frame) {
	c.mu.Lock()
	p, ok := c.pending[f.ID]
	c.mu.Unlock()
	if !ok {
		// A reply for a call that already timed out or was cancelled:
		// expected after a deadline, so drop it quietly.
		return
	}
	switch f.Type {
	case "response":
		body, err := decodeBody(f.Body)
		if err != nil {
			c.finish(f.ID, p, startInfo{err: fmt.Errorf("remote: bad response body: %w", err)}, err)
			return
		}
		select {
		case p.start <- startInfo{status: statusOr(f.Status), headers: cloneHeaders(f.Headers)}:
		case <-p.ctxDone:
			return
		case <-ctx.Done():
			return
		}
		if len(body) > 0 {
			select {
			case p.chunks <- body:
			case <-p.ctxDone:
				return
			case <-ctx.Done():
				return
			}
		}
		c.finish(f.ID, p, startInfo{}, nil)
	case "response_start":
		select {
		case p.start <- startInfo{status: statusOr(f.Status), headers: cloneHeaders(f.Headers)}:
		case <-p.ctxDone:
		case <-ctx.Done():
		}
	case "response_chunk":
		data, err := decodeBody(f.Data)
		if err != nil {
			c.finish(f.ID, p, startInfo{}, fmt.Errorf("remote: bad chunk: %w", err))
			return
		}
		// Bounded queue: a slow consumer blocks this read loop,
		// which backpressures the sidecar over the socket instead
		// of buffering a whole file in Ether's memory.
		select {
		case p.chunks <- data:
		case <-p.ctxDone:
		case <-ctx.Done():
		}
	case "response_end":
		c.finish(f.ID, p, startInfo{}, nil)
	case "response_error", "error":
		err := f.Error.error()
		c.finish(f.ID, p, startInfo{err: err}, err)
	default:
		c.server.log.Debug("plugin tunnel unknown frame", "plugin", c.pluginID, "type", f.Type)
	}
}

// finish completes a pending call. A startInfo with an error is also
// offered on start for callers still waiting for headers.
func (c *tunnelConn) finish(id string, p *pendingCall, start startInfo, err error) {
	c.mu.Lock()
	if c.pending[id] == p {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if start.err != nil {
		select {
		case p.start <- start:
		default:
		}
	}
	select {
	case p.done <- err:
	default:
	}
}

func statusOr(s int) int {
	if s == 0 {
		return http.StatusOK
	}
	return s
}

// begin registers and sends one request, returning its pending call.
func (c *tunnelConn) begin(ctx context.Context, req Request, credential string) (*pendingCall, string, context.CancelFunc, error) {
	if err := validatePath(req.Path); err != nil {
		return nil, "", nil, err
	}
	// Backpressure: at most MaxInFlight concurrent calls per socket.
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, "", nil, ctx.Err()
	}
	release := func() { <-c.sem }

	ctx, cancel, deadline := deadlineFor(ctx, req, c.server.timeout)
	done := func() { cancel(); release() }

	c.mu.Lock()
	if c.dead != nil {
		dead := c.dead
		c.mu.Unlock()
		done()
		return nil, "", nil, dead
	}
	c.nextID++
	id := fmt.Sprintf("%d", c.nextID)
	p := &pendingCall{
		start:   make(chan startInfo, 1),
		chunks:  make(chan []byte, streamChunkQueueSize),
		done:    make(chan error, 1),
		ctxDone: ctx.Done(),
	}
	c.pending[id] = p
	c.mu.Unlock()

	requestID := req.RequestID
	if requestID == "" {
		requestID = newRequestID()
	}
	method := strings.ToUpper(strings.TrimSpace(req.Method))
	if method == "" {
		method = http.MethodGet
	}
	f := frame{
		Type:      "request",
		ID:        id,
		RequestID: requestID,
		Method:    method,
		Path:      req.Path,
		Query:     req.Query,
		Headers:   etherHeaders(req, requestID, req.Locale, credential, deadline),
		Body:      encodeBody(req.Body),
	}
	if !deadline.IsZero() {
		f.Deadline = deadline.UTC().Format(time.RFC3339)
	}

	// Deadline watcher: drop the pending entry and tell the sidecar
	// to stop work nobody is waiting for.
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		_, live := c.pending[id]
		if live {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		if live {
			_ = c.write(context.Background(), frame{Type: "cancel", ID: id})
			select {
			case p.start <- startInfo{err: ctx.Err()}:
			default:
			}
			select {
			case p.done <- ctx.Err():
			default:
			}
		}
	}()

	if err := c.write(ctx, f); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		done()
		if ctx.Err() != nil {
			return nil, "", nil, ctx.Err()
		}
		return nil, "", nil, fmt.Errorf("%w: %v", ErrDisconnected, err)
	}
	return p, id, done, nil
}

// call buffers one reply up to the size cap.
func (c *tunnelConn) call(ctx context.Context, req Request, credential string) (Response, error) {
	stream, err := c.callStream(ctx, req, credential)
	if err != nil {
		return Response{}, err
	}
	defer stream.Body.Close()
	body, err := readCapped(stream.Body, c.server.maxBytes)
	if err != nil {
		return Response{}, err
	}
	return Response{Status: stream.Status, Headers: stream.Headers, Body: body}, nil
}

// callStream returns as soon as the sidecar sends response headers;
// the body then streams chunk by chunk.
func (c *tunnelConn) callStream(ctx context.Context, req Request, credential string) (*StreamResponse, error) {
	p, _, done, err := c.begin(ctx, req, credential)
	if err != nil {
		return nil, err
	}
	select {
	case start := <-p.start:
		if start.err != nil {
			done()
			return nil, start.err
		}
		return &StreamResponse{
			Status:  start.status,
			Headers: start.headers,
			Body:    &tunnelBody{p: p, done: done},
		}, nil
	case err := <-p.done:
		// If response headers and body arrived together before we read
		// p.start, drain p.start first so a fast response is not lost.
		select {
		case start := <-p.start:
			if start.err == nil && err == nil {
				return &StreamResponse{
					Status:  start.status,
					Headers: start.headers,
					Body:    &tunnelBody{p: p, done: done, ended: true},
				}, nil
			}
			if start.err != nil {
				err = start.err
			}
		default:
		}
		done()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
}

// tunnelBody adapts chunk frames to io.ReadCloser.
type tunnelBody struct {
	p      *pendingCall
	done   context.CancelFunc
	buf    []byte
	ended  bool
	err    error
	closed bool
}

func (b *tunnelBody) Read(out []byte) (int, error) {
	for len(b.buf) == 0 {
		if b.closed {
			return 0, io.ErrClosedPipe
		}
		// Always drain buffered chunks before acting on a terminal done
		// signal, because Go's select picks randomly when both chunks
		// and done are ready.
		select {
		case chunk := <-b.p.chunks:
			b.buf = chunk
			continue
		default:
		}
		if b.err != nil {
			return 0, b.err
		}
		if b.ended {
			return 0, io.EOF
		}
		select {
		case chunk := <-b.p.chunks:
			b.buf = chunk
		case err := <-b.p.done:
			b.ended = true
			if err != nil {
				b.err = err
			}
		}
	}
	n := copy(out, b.buf)
	b.buf = b.buf[n:]
	return n, nil
}

func (b *tunnelBody) Close() error {
	if b.closed {
		return nil
	}
	b.closed = true
	// Cancelling the call context makes begin's watcher send a
	// cancel frame, so the sidecar stops producing chunks.
	b.done()
	return nil
}
