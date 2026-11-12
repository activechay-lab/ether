package etherplugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// errUnauthorized marks a tunnel handshake a server rejected with 401.
var errUnauthorized = errors.New("etherplugin: tunnel credential rejected")

const (
	// TunnelProtocol is the WebSocket subprotocol for Ether remote plugin tunnels.
	TunnelProtocol = "ether-tunnel-v1"
	// DefaultMaxChunkBytes is the default chunk size for streamed file responses.
	DefaultMaxChunkBytes = 64 * 1024
)

// TunnelError carries an error payload inside a TunnelFrame.
type TunnelError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// TunnelFrame is one JSON message sent or received over the `ether-tunnel-v1` WebSocket.
type TunnelFrame struct {
	Type          string              `json:"type"`
	ID            string              `json:"id,omitempty"`
	RequestID     string              `json:"requestId,omitempty"`
	Protocol      string              `json:"protocol,omitempty"`
	Plugin        string              `json:"plugin,omitempty"`
	PluginID      string              `json:"pluginId,omitempty"`
	ConnectionID  string              `json:"connectionId,omitempty"`
	Features      []string            `json:"features,omitempty"`
	MaxChunkBytes int                 `json:"maxChunkBytes,omitempty"`
	Method        string              `json:"method,omitempty"`
	Path          string              `json:"path,omitempty"`
	Query         map[string][]string `json:"query,omitempty"`
	Headers       map[string][]string `json:"headers,omitempty"`
	Body          string              `json:"body,omitempty"`
	Data          string              `json:"data,omitempty"`
	Status        int                 `json:"status,omitempty"`
	Deadline      string              `json:"deadline,omitempty"`
	Error         *TunnelError        `json:"error,omitempty"`
}

// DispatchTunnelFrame executes a `"request"` TunnelFrame against a Plugin and
// returns the outgoing response frames (either a single `"response"` frame or a
// `"response_start"` / `"response_chunk"` / `"response_end"` sequence for
// `/ether/file/*` binary responses).
func (p *Plugin) DispatchTunnelFrame(ctx context.Context, frame TunnelFrame, maxChunkBytes int) []TunnelFrame {
	if frame.Type != "request" || frame.ID == "" {
		return nil
	}
	if maxChunkBytes <= 0 {
		maxChunkBytes = DefaultMaxChunkBytes
	}

	var decodedBody []byte
	if frame.Body != "" {
		b, err := base64.StdEncoding.DecodeString(frame.Body)
		if err != nil {
			return []TunnelFrame{{
				Type:  "error",
				ID:    frame.ID,
				Error: &TunnelError{Code: "bad_request", Message: "Invalid base64 request body."},
			}}
		}
		decodedBody = b
	}

	hdrs := map[string]string{}
	for k, vals := range frame.Headers {
		if len(vals) > 0 {
			hdrs[k] = vals[0]
		}
	}
	if frame.RequestID != "" && hdrs["X-Ether-Request-ID"] == "" {
		hdrs["X-Ether-Request-ID"] = frame.RequestID
	}
	query := map[string]string{}
	for k, vals := range frame.Query {
		if len(vals) > 0 {
			query[k] = vals[0]
		}
	}

	res := p.Dispatch(ctx, RawRequest{
		Method:  frame.Method,
		Path:    frame.Path,
		Query:   query,
		Headers: hdrs,
		Body:    decodedBody,
	})

	multiHeaders := map[string][]string{}
	for k, v := range res.Headers {
		multiHeaders[k] = []string{v}
	}

	if res.BinaryBody != nil && strings.HasPrefix(frame.Path, "/ether/file/") {
		out := []TunnelFrame{{
			Type:    "response_start",
			ID:      frame.ID,
			Status:  res.Status,
			Headers: multiHeaders,
		}}
		limit := maxChunkBytes
		if limit < 1024 {
			limit = 1024
		}
		for offset := 0; offset < len(res.BinaryBody); offset += limit {
			end := offset + limit
			if end > len(res.BinaryBody) {
				end = len(res.BinaryBody)
			}
			out = append(out, TunnelFrame{
				Type: "response_chunk",
				ID:   frame.ID,
				Data: base64.StdEncoding.EncodeToString(res.BinaryBody[offset:end]),
			})
		}
		out = append(out, TunnelFrame{
			Type: "response_end",
			ID:   frame.ID,
		})
		return out
	}

	payload := res.Body
	if res.BinaryBody != nil {
		payload = res.BinaryBody
	}
	var encodedBody string
	if len(payload) > 0 {
		encodedBody = base64.StdEncoding.EncodeToString(payload)
	}
	return []TunnelFrame{{
		Type:    "response",
		ID:      frame.ID,
		Status:  res.Status,
		Headers: multiHeaders,
		Body:    encodedBody,
	}}
}

func (p *Plugin) startTunnel(ctx context.Context, tunnelURL, credential string, fromFile bool, stateDir string) (*ServeHandle, error) {
	if tunnelURL == "" {
		return nil, fmt.Errorf("tunnel mode requires TunnelURL or ETHER_TUNNEL_URL")
	}
	loopCtx, cancel := context.WithCancel(ctx)
	conn, maxChunk, err := p.dialTunnel(loopCtx, tunnelURL, credential)
	if err != nil {
		cancel()
		if errors.Is(err, errUnauthorized) && fromFile {
			clearSavedCredential(p.manifest.ID, stateDir)
			return nil, noCredentialError()
		}
		return nil, err
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer cancel()
		backoff := 100 * time.Millisecond
		for {
			started := time.Now()
			p.runTunnel(loopCtx, conn, maxChunk)
			if time.Since(started) >= time.Minute {
				backoff = 100 * time.Millisecond
			}
			for {
				// Equal jitter keeps retries bounded and avoids synchronized sidecars.
				delay := backoff/2 + time.Duration(rand.Int64N(int64(backoff/2)))
				timer := time.NewTimer(delay)
				select {
				case <-loopCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if backoff < 10*time.Second {
					backoff = min(backoff*2, 10*time.Second)
				}
				conn, maxChunk, err = p.dialTunnel(loopCtx, tunnelURL, credential)
				if err == nil {
					break
				}
				// A saved credential the server no longer accepts is dead:
				// clear it and stop rather than reconnect with it forever.
				if errors.Is(err, errUnauthorized) && fromFile {
					clearSavedCredential(p.manifest.ID, stateDir)
					return
				}
			}
		}
	}()
	return &ServeHandle{
		Done: done,
		Mode: "tunnel",
		URL:  tunnelURL,
		Close: func() error {
			cancel()
			<-done
			return nil
		},
	}, nil
}

// dialTunnel authenticates and completes the handshake on every attempt.
// Its timeout also bounds a peer that accepts a socket but never welcomes us.
func (p *Plugin) dialTunnel(ctx context.Context, tunnelURL, credential string) (*websocket.Conn, int, error) {
	handshakeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	dialOpts := &websocket.DialOptions{
		Subprotocols: []string{TunnelProtocol},
		HTTPHeader:   http.Header{},
	}
	if credential != "" {
		dialOpts.HTTPHeader.Set("X-Ether-Plugin-Credential", credential)
	}
	conn, resp, err := websocket.Dial(handshakeCtx, tunnelURL, dialOpts)
	if err != nil {
		if resp != nil {
			if resp.Body != nil {
				resp.Body.Close()
			}
			if resp.StatusCode == http.StatusUnauthorized {
				return nil, 0, fmt.Errorf("%w (HTTP %d)", errUnauthorized, resp.StatusCode)
			}
		}
		return nil, 0, err
	}
	conn.SetReadLimit(16 << 20)
	helloBytes, _ := json.Marshal(TunnelFrame{
		Type: "hello", Protocol: TunnelProtocol, Plugin: p.manifest.ID,
		Features: []string{"range", "stream"},
	})
	if err := conn.Write(handshakeCtx, websocket.MessageText, helloBytes); err != nil {
		conn.CloseNow()
		return nil, 0, err
	}
	_, welcomeBytes, err := conn.Read(handshakeCtx)
	if err != nil {
		conn.CloseNow()
		return nil, 0, err
	}
	var welcome TunnelFrame
	if err := json.Unmarshal(welcomeBytes, &welcome); err != nil || welcome.Type != "welcome" {
		conn.CloseNow()
		return nil, 0, fmt.Errorf("unexpected tunnel handshake response: %s", string(welcomeBytes))
	}
	maxChunk := welcome.MaxChunkBytes
	if maxChunk <= 0 {
		maxChunk = DefaultMaxChunkBytes
	}
	return conn, maxChunk, nil
}

// runTunnel owns one socket. Only the supervisor dials; old handlers retain
// their old socket and are cancelled before a replacement is opened.
func (p *Plugin) runTunnel(ctx context.Context, conn *websocket.Conn, maxChunk int) {
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.CloseNow()
	var mu sync.Mutex
	active := make(map[string]context.CancelFunc)
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, stop := range active {
			stop()
		}
	}()
	for {
		_, msgBytes, err := conn.Read(loopCtx)
		if err != nil {
			return
		}
		var frame TunnelFrame
		if json.Unmarshal(msgBytes, &frame) != nil {
			continue
		}
		if frame.Type == "cancel" {
			mu.Lock()
			stop := active[frame.ID]
			mu.Unlock()
			if stop != nil {
				stop()
			}
			continue
		}
		if frame.Type != "request" {
			continue
		}
		requestCtx, stop := context.WithCancel(loopCtx)
		mu.Lock()
		active[frame.ID] = stop
		mu.Unlock()
		go func(frame TunnelFrame) {
			defer func() { mu.Lock(); delete(active, frame.ID); mu.Unlock(); stop() }()
			for _, reply := range p.DispatchTunnelFrame(requestCtx, frame, maxChunk) {
				if requestCtx.Err() != nil {
					return
				}
				b, err := json.Marshal(reply)
				if err != nil {
					return
				}
				// WebSocket cancellation is connection-fatal. A per-request cancel
				// must stop dispatch, never interrupt a write on the shared socket.
				writeCtx, writeCancel := context.WithTimeout(loopCtx, 10*time.Second)
				err = conn.Write(writeCtx, websocket.MessageText, b)
				writeCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}(frame)
	}
}
