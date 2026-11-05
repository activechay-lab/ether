package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// HandlerFunc serves one tunneled plugin request on the sidecar. It
// mirrors the hosted runtime ABI (SPEC §5.2): request in, response
// out. A handler that needs to stream a large body (a file range)
// implements StreamHandlerFunc instead; the client picks it when
// set.
type HandlerFunc func(ctx context.Context, req Request) (Response, error)

// StreamHandlerFunc serves a request with a streaming body. Return
// Handled=false (via a nil StreamResponse and ErrStreamNotHandled)
// to fall back to the buffered handler.
type StreamHandlerFunc func(ctx context.Context, req Request) (*StreamResponse, error)

// ErrStreamNotHandled asks the tunnel client to use its buffered
// handler for a request.
var ErrStreamNotHandled = errors.New("remote: stream handler declined")

// TunnelClient is the Go reference sidecar: it connects out to Ether
// over one websocket, authenticates with a plugin credential, and
// dispatches multiplexed requests to a local handler. It exists for
// the conformance kit, for Go sidecars, and as the executable
// reference the Python and TypeScript fixtures mirror.
type TunnelClient struct {
	url        string
	credential string
	plugin     string
	handler    HandlerFunc
	stream     StreamHandlerFunc
	chunkBytes int
	log        *slog.Logger
	// dialOptions is a test seam; nil uses defaults.
	dialOptions *websocket.DialOptions
}

// TunnelClientOptions configures a TunnelClient.
type TunnelClientOptions struct {
	// URL is Ether's tunnel endpoint, ws:// or wss:// (http/https
	// are accepted and rewritten).
	URL string
	// Credential authenticates the connection (sent as
	// X-Ether-Plugin-Credential on the upgrade request).
	Credential string
	// Plugin, when set, declares the plugin id in the hello frame.
	// It must match the credential's plugin or Ether refuses the
	// connection, which catches a sidecar started with the wrong
	// credential early.
	Plugin string
	// Handler serves buffered replies. Required.
	Handler HandlerFunc
	// StreamHandler, when set, gets first refusal on every request
	// (file ranges use it through FileHandler).
	StreamHandler StreamHandlerFunc
	Logger        *slog.Logger
}

// NewTunnelClient builds a sidecar client. Serve it with
// ServeOnce or Serve (reconnecting).
func NewTunnelClient(opts TunnelClientOptions) (*TunnelClient, error) {
	if opts.URL == "" || opts.Credential == "" {
		return nil, fmt.Errorf("%w: tunnel URL and credential are required", ErrInvalid)
	}
	if opts.Handler == nil && opts.StreamHandler == nil {
		return nil, fmt.Errorf("%w: a handler is required", ErrInvalid)
	}
	u := opts.URL
	switch {
	case strings.HasPrefix(u, "https://"):
		u = "wss://" + strings.TrimPrefix(u, "https://")
	case strings.HasPrefix(u, "http://"):
		u = "ws://" + strings.TrimPrefix(u, "http://")
	}
	log := opts.Logger
	if log == nil {
		log = slog.Default()
	}
	return &TunnelClient{
		url:        u,
		credential: opts.Credential,
		plugin:     opts.Plugin,
		handler:    opts.Handler,
		stream:     opts.StreamHandler,
		chunkBytes: DefaultChunkBytes,
		log:        log,
	}, nil
}

// ServeOnce serves one tunnel connection until it ends.
func (c *TunnelClient) ServeOnce(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	opts := c.dialOptions
	if opts == nil {
		opts = &websocket.DialOptions{}
	}
	clone := *opts
	if clone.HTTPHeader == nil {
		clone.HTTPHeader = http.Header{}
	} else {
		clone.HTTPHeader = clone.HTTPHeader.Clone()
	}
	clone.HTTPHeader.Set(HeaderCredential, c.credential)
	clone.Subprotocols = []string{TunnelProtocol}
	ws, resp, err := websocket.Dial(dialCtx, c.url, &clone)
	cancel()
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return ErrUnauthorized
		}
		return err
	}
	defer ws.Close(websocket.StatusNormalClosure, "sidecar stopping")
	ws.SetReadLimit(defaultReadLimit)

	ctx, cancel = context.WithCancel(ctx)
	defer cancel()
	cc := &clientConn{
		client:  c,
		ws:      ws,
		cancels: map[string]context.CancelFunc{},
	}
	if err := cc.write(ctx, frame{
		Type:     "hello",
		Protocol: TunnelProtocol,
		Plugin:   c.plugin,
		Features: []string{"range", "stream"},
	}); err != nil {
		return err
	}
	// The welcome frame is consumed by the read loop below like any
	// other frame; read it here so a protocol refusal surfaces as a
	// connect error rather than a later hang.
	welcomeCtx, welcomeCancel := context.WithTimeout(ctx, defaultHelloTimeout)
	_, raw, err := ws.Read(welcomeCtx)
	welcomeCancel()
	if err != nil {
		return err
	}
	var welcome frame
	if json.Unmarshal(raw, &welcome) != nil || welcome.Type != "welcome" {
		return fmt.Errorf("remote: expected welcome frame")
	}
	if welcome.MaxChunk > 0 {
		cc.chunkBytes = welcome.MaxChunk
	} else {
		cc.chunkBytes = c.chunkBytes
	}
	c.log.Info("plugin tunnel connected", "plugin", welcome.PluginID, "connection", welcome.ConnID)
	return cc.readLoop(ctx)
}

type clientConn struct {
	client     *TunnelClient
	ws         *websocket.Conn
	chunkBytes int
	writeMu    sync.Mutex

	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func (c *clientConn) write(ctx context.Context, f frame) error {
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

func (c *clientConn) readLoop(ctx context.Context) error {
	// When the socket ends, in-flight handlers can no longer reply:
	// cancel them before waiting, so a handler blocked on its context
	// does not hold up ServeOnce (and Serve's reconnect) until its
	// own deadline.
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer wg.Wait()
	defer cancel()
	for {
		_, raw, err := c.ws.Read(ctx)
		if err != nil {
			return err
		}
		var f frame
		if json.Unmarshal(raw, &f) != nil {
			continue
		}
		switch f.Type {
		case "request":
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.serveRequest(ctx, f)
			}()
		case "cancel":
			c.mu.Lock()
			cancel := c.cancels[f.ID]
			c.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
	}
}

func (c *clientConn) serveRequest(connCtx context.Context, f frame) {
	ctx := connCtx
	if f.Deadline != "" {
		if dl, err := time.Parse(time.RFC3339, f.Deadline); err == nil {
			var cancel context.CancelFunc
			ctx, cancel = context.WithDeadline(connCtx, dl)
			defer cancel()
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c.mu.Lock()
	c.cancels[f.ID] = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.cancels, f.ID)
		c.mu.Unlock()
	}()

	body, err := decodeBody(f.Body)
	if err != nil {
		_ = c.write(connCtx, frame{Type: "error", ID: f.ID, Error: &frameError{Code: "bad_request", Message: "request body is not valid base64"}})
		return
	}
	req := Request{
		Method:    f.Method,
		Path:      f.Path,
		Query:     f.Query,
		Headers:   f.Headers,
		Body:      body,
		Locale:    firstHeader(f.Headers, HeaderLocale),
		RequestID: f.RequestID,
	}

	// Streaming handlers get first refusal so file ranges never need
	// buffering on either side.
	if c.client.stream != nil {
		stream, err := c.client.stream(ctx, req)
		if err != nil && !errors.Is(err, ErrStreamNotHandled) {
			_ = c.write(connCtx, errorFrame(f.ID, err))
			return
		}
		if err == nil && stream != nil {
			c.writeStream(connCtx, ctx, f.ID, stream)
			return
		}
	}
	handler := c.client.handler
	if handler == nil {
		_ = c.write(connCtx, frame{Type: "error", ID: f.ID, Error: &frameError{Code: "not_found", Message: "no handler for this route"}})
		return
	}
	resp, err := handler(ctx, req)
	if err != nil {
		_ = c.write(connCtx, errorFrame(f.ID, err))
		return
	}
	_ = c.write(connCtx, frame{
		Type:    "response",
		ID:      f.ID,
		Status:  statusOr(resp.Status),
		Headers: resp.Headers,
		Body:    encodeBody(resp.Body),
	})
}

func errorFrame(id string, err error) frame {
	code := "error"
	switch {
	case errors.Is(err, ErrNotFound):
		code = "not_found"
	case errors.Is(err, ErrPermissionDenied):
		code = "permission_denied"
	case errors.Is(err, ErrInvalid):
		code = "invalid"
	case errors.Is(err, context.DeadlineExceeded):
		code = "deadline"
	}
	return frame{Type: "error", ID: id, Error: &frameError{Code: code, Message: err.Error()}}
}

func (c *clientConn) writeStream(connCtx, reqCtx context.Context, id string, stream *StreamResponse) {
	defer stream.Body.Close()
	if err := c.write(connCtx, frame{
		Type: "response_start", ID: id,
		Status: statusOr(stream.Status), Headers: stream.Headers,
	}); err != nil {
		return
	}
	chunk := c.chunkBytes
	if chunk <= 0 {
		chunk = DefaultChunkBytes
	}
	buf := make([]byte, chunk)
	for {
		if reqCtx.Err() != nil {
			return // cancelled; Ether has already dropped the call
		}
		n, err := stream.Body.Read(buf)
		if n > 0 {
			if werr := c.write(connCtx, frame{
				Type: "response_chunk", ID: id,
				Data: encodeBody(buf[:n]),
			}); werr != nil {
				return
			}
		}
		if err == io.EOF {
			_ = c.write(connCtx, frame{Type: "response_end", ID: id})
			return
		}
		if err != nil {
			_ = c.write(connCtx, frame{Type: "response_error", ID: id,
				Error: &frameError{Code: "stream_error", Message: err.Error()}})
			return
		}
	}
}

// FileHandler serves files under root for requests to
// /ether/file/<path>, honouring one Range header
// (bytes=start-end, bytes=start-, bytes=-suffix). It is how a
// sidecar on another machine serves `file` streams (SPEC §5.1.4):
// Ether relays the range over the tunnel in chunks and the sidecar
// never needs an inbound port. Paths cannot escape root, and only
// regular files are served. Use it as a TunnelClient StreamHandler.
func FileHandler(root string) StreamHandlerFunc {
	root = filepath.Clean(root)
	return func(ctx context.Context, req Request) (*StreamResponse, error) {
		if req.Method != "" && req.Method != http.MethodGet && req.Method != http.MethodHead {
			return nil, ErrStreamNotHandled
		}
		rel := strings.TrimPrefix(req.Path, "/ether/file/")
		if rel == req.Path || rel == "" {
			return nil, ErrStreamNotHandled
		}
		if requestedRoot := firstHeader(req.Headers, "X-Ether-File-Root"); requestedRoot != "" && filepath.Clean(requestedRoot) != root {
			return nil, ErrPermissionDenied
		}
		// Open through a rooted descriptor so a concurrent symlink swap cannot
		// move the opened file outside the configured root.
		for _, component := range strings.Split(rel, "/") {
			if component == ".." {
				return nil, ErrInvalid
			}
		}
		rootFS, err := os.OpenRoot(root)
		if err != nil {
			return nil, ErrNotFound
		}
		f, err := rootFS.Open(filepath.FromSlash(rel))
		_ = rootFS.Close()
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ErrNotFound
			}
			return nil, fmt.Errorf("%w: invalid file path", ErrInvalid)
		}
		info, err := f.Stat()
		if err != nil || !info.Mode().IsRegular() {
			f.Close()
			return nil, ErrNotFound
		}
		size := info.Size()
		start, end, ranged, err := parseByteRange(firstHeader(req.Headers, "Range"), size)
		if err != nil {
			f.Close()
			return &StreamResponse{
				Status:  http.StatusRequestedRangeNotSatisfiable,
				Headers: map[string][]string{"Content-Range": {fmt.Sprintf("bytes */%d", size)}},
				Body:    io.NopCloser(strings.NewReader("")),
			}, nil
		}
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			f.Close()
			return nil, err
		}
		length := end - start + 1
		headers := map[string][]string{
			"Accept-Ranges":  {"bytes"},
			"Content-Length": {strconv.FormatInt(length, 10)},
		}
		status := http.StatusOK
		if ranged {
			status = http.StatusPartialContent
			headers["Content-Range"] = []string{fmt.Sprintf("bytes %d-%d/%d", start, end, size)}
		}
		if req.Method == http.MethodHead {
			f.Close()
			return &StreamResponse{Status: status, Headers: headers, Body: io.NopCloser(strings.NewReader(""))}, nil
		}
		return &StreamResponse{
			Status:  status,
			Headers: headers,
			Body:    &limitedFile{file: f, left: length},
		}, nil
	}
}

type limitedFile struct {
	file *os.File
	left int64
}

func (l *limitedFile) Read(p []byte) (int, error) {
	if l.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > l.left {
		p = p[:l.left]
	}
	n, err := l.file.Read(p)
	l.left -= int64(n)
	return n, err
}

func (l *limitedFile) Close() error { return l.file.Close() }

// parseByteRange parses a single-range Range header against size.
// An empty header selects the whole file. Multi-range requests are
// refused: plugin file relay only ever needs one range.
func parseByteRange(header string, size int64) (start, end int64, ranged bool, err error) {
	if header == "" {
		return 0, size - 1, false, nil
	}
	if !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return 0, 0, false, fmt.Errorf("%w: unsupported range", ErrInvalid)
	}
	spec := strings.TrimPrefix(header, "bytes=")
	dash := strings.Index(spec, "-")
	if dash < 0 || size == 0 {
		return 0, 0, false, fmt.Errorf("%w: unsatisfiable range", ErrInvalid)
	}
	lo, hi := spec[:dash], spec[dash+1:]
	switch {
	case lo == "" && hi == "":
		return 0, 0, false, fmt.Errorf("%w: unsatisfiable range", ErrInvalid)
	case lo == "":
		suffix, perr := strconv.ParseInt(hi, 10, 64)
		if perr != nil || suffix <= 0 {
			return 0, 0, false, fmt.Errorf("%w: unsatisfiable range", ErrInvalid)
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, nil
	default:
		start, perr := strconv.ParseInt(lo, 10, 64)
		if perr != nil || start < 0 || start >= size {
			return 0, 0, false, fmt.Errorf("%w: unsatisfiable range", ErrInvalid)
		}
		end = size - 1
		if hi != "" {
			end, perr = strconv.ParseInt(hi, 10, 64)
			if perr != nil || end < start {
				return 0, 0, false, fmt.Errorf("%w: unsatisfiable range", ErrInvalid)
			}
			if end >= size {
				end = size - 1
			}
		}
		return start, end, true, nil
	}
}
