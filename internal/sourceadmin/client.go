package sourceadmin

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/nodewire/outbound"
)

type ClientOptions struct {
	Guard *nodewire.Guard
	Peer  nodewire.Identity
	// Endpoint is an explicit origin (https://host:port), never caller input.
	Endpoint string
	// Authorize checks current exact local server and target source roles and
	// current resource eligibility before each dial. Guard additionally checks
	// fresh remote incarnation/revocation before any HTTP headers are sent.
	Authorize func(context.Context, nodewire.Identity, Request) error
	Timeout   time.Duration
}
type Client struct {
	o        ClientOptions
	http     *http.Client
	endpoint string
}

func identityValid(i nodewire.Identity) bool { return id(i.Cluster) && id(i.NodeID) && id(i.Boot) }

func NewClient(o ClientOptions) (*Client, error) {
	if o.Guard == nil || !identityValid(o.Peer) || o.Authorize == nil || !timeoutValid(o.Timeout) {
		return nil, ErrInvalid
	}
	u, err := url.Parse(o.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return nil, ErrInvalid
	}
	tc, err := o.Guard.ClientTLS(o.Peer, u.Hostname())
	if err != nil {
		return nil, ErrInvalid
	}
	tr, err := outbound.New(o.Endpoint, tc, o.Timeout, func(ctx context.Context, cs tls.ConnectionState) error {
		return o.Guard.RecheckServer(ctx, cs, o.Peer, u.Hostname())
	})
	if err != nil {
		return nil, ErrInvalid
	}
	c := &http.Client{Transport: tr, Timeout: o.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &Client{o, c, "https://" + u.Host + Path}, nil
}

// wireContext retains deadlines/cancellation but hides upstream context values,
// including httptrace hooks which can observe outbound credential headers.
type wireContext struct{ context.Context }

func (wireContext) Value(any) any { return nil }

func (c *Client) Call(ctx context.Context, credential string, r Request) (Response, error) {
	var out Response
	if !credentialValid(credential) || !validRequest(r) {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, c.o.Timeout)
	defer cancel()
	if c.o.Authorize(ctx, c.o.Peer, r) != nil {
		return out, ErrDenied
	}
	if ctx.Err() != nil {
		return out, ErrUnavailable
	}
	data, err := json.Marshal(r)
	if err != nil || len(data) > maxRequest {
		return out, ErrInvalid
	}
	req, err := http.NewRequestWithContext(wireContext{ctx}, http.MethodPost, c.endpoint, bytes.NewReader(data))
	if err != nil {
		return out, ErrInvalid
	}
	req.Header.Set(credentialHeader, credential)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return out, ErrUnavailable
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden:
		return out, ErrDenied
	case http.StatusBadRequest:
		return out, ErrInvalid
	case http.StatusNotImplemented:
		return out, ErrUnsupported
	default:
		return out, ErrUnavailable
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse || bytes.Contains(data, []byte(credential)) || decode(data, &out) != nil || !validResponse(r, out) {
		return Response{}, ErrUnavailable
	}
	return out, nil
}
func (c *Client) CloseIdleConnections() { c.http.CloseIdleConnections() }
