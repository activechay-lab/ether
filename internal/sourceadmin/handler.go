package sourceadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"
)

type Handler struct{ o Options }

func NewHandler(o Options) (*Handler, error) {
	if o.Guard == nil || !identityValid(o.Local) || o.Authorize == nil || o.VerifyOwner == nil || !timeoutValid(o.Timeout) {
		return nil, ErrInvalid
	}
	return &Handler{o}, nil
}
func timeoutValid(t time.Duration) bool { return t > 0 && t <= 30*time.Second }
func id(s string) bool {
	if len(s) == 0 || len(s) > 128 || s == "." || s == ".." {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func credentialValid(s string) bool {
	if len(s) == 0 || len(s) > 4096 {
		return false
	}
	for _, c := range s {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func validRequest(r Request) bool {
	if !id(r.RequestID) {
		return false
	}
	switch r.Operation {
	case TestProvider, TestIndexer, TestDebrid, CacheStatus:
		return id(r.ResourceID) && r.Payload == nil
	case Snapshot, Traces, ReleaseHistory, Variants:
		if r.ResourceID != "" || r.Payload == nil || r.Payload.Limit < 1 || r.Payload.Limit > MaxItems {
			return false
		}
		if r.Operation == ReleaseHistory || r.Operation == Variants {
			return r.Payload.TitleID > 0
		}
		return r.Payload.TitleID == 0
	}
	return false
}
func decode(data []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return ErrInvalid
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path != Path || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.Method != http.MethodPost {
		writeError(w, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.o.Timeout)
	defer cancel()
	r = r.WithContext(ctx)
	caller, err := h.o.Guard.Request(r)
	if err != nil {
		writeError(w, ErrDenied)
		return
	}
	credentials := r.Header.Values(credentialHeader)
	if len(credentials) != 1 || !credentialValid(credentials[0]) {
		writeError(w, ErrDenied)
		return
	}
	// Never retain credentials in the request passed into dependency callbacks.
	credential := credentials[0]
	r.Header.Del(credentialHeader)
	if h.o.VerifyOwner(ctx, credential) != nil {
		writeError(w, ErrDenied)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequest))
	if err != nil {
		writeError(w, ErrInvalid)
		return
	}
	var req Request
	if decode(data, &req) != nil || !validRequest(req) {
		writeError(w, ErrInvalid)
		return
	}
	if h.o.Authorize(ctx, caller, h.o.Local, req) != nil {
		writeError(w, ErrDenied)
		return
	}
	resp, err := h.execute(ctx, req)
	if ctx.Err() != nil {
		err = ErrUnavailable
	}
	if err != nil {
		writeError(w, err)
		return
	}
	if !validResponse(req, resp) {
		writeError(w, ErrUnavailable)
		return
	}
	data, err = json.Marshal(resp)
	// Even an accidentally numeric credential must never appear in a response.
	if err != nil || len(data) > maxResponse || bytes.Contains(data, []byte(credential)) {
		writeError(w, ErrUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func (h *Handler) execute(ctx context.Context, r Request) (Response, error) {
	s := h.o.Services
	var out Response
	var err error
	switch r.Operation {
	case Snapshot:
		if s.Snapshot == nil {
			return out, ErrUnsupported
		}
		var v Diagnostics
		v, err = s.Snapshot(ctx, r.Payload.Limit)
		out.Diagnostics = &v
	case TestProvider, TestIndexer, TestDebrid:
		fn := s.TestProvider
		if r.Operation == TestIndexer {
			fn = s.TestIndexer
		}
		if r.Operation == TestDebrid {
			fn = s.TestDebrid
		}
		if fn == nil {
			return out, ErrUnsupported
		}
		err = fn(ctx, r.ResourceID)
		out.Tested = err == nil
	case CacheStatus:
		if s.CacheStatus == nil {
			return out, ErrUnsupported
		}
		var v Cache
		v, err = s.CacheStatus(ctx, r.ResourceID)
		out.Cache = &v
	case Traces:
		if s.Traces == nil {
			return out, ErrUnsupported
		}
		out.Traces, err = s.Traces(ctx, *r.Payload)
	case ReleaseHistory, Variants:
		fn := s.ReleaseHistory
		if r.Operation == Variants {
			fn = s.Variants
		}
		if fn == nil {
			return out, ErrUnsupported
		}
		out.Releases, err = fn(ctx, *r.Payload)
	default:
		return out, ErrInvalid
	}
	return out, err
}
func stateValid(s Status) bool { return s == Healthy || s == Backoff || s == Failed || s == Unknown }
func validResponse(r Request, v Response) bool {
	limit := MaxItems
	if r.Payload != nil {
		limit = r.Payload.Limit
	}
	if len(v.Traces) > limit || len(v.Releases) > limit {
		return false
	}
	for _, t := range v.Traces {
		if !stateValid(t.State) {
			return false
		}
	}
	for _, t := range v.Releases {
		if !stateValid(t.State) || r.Payload == nil || t.TitleID != r.Payload.TitleID {
			return false
		}
	}
	if d := v.Diagnostics; d != nil {
		if len(d.Providers) > limit || len(d.Indexers) > limit {
			return false
		}
		for _, p := range d.Providers {
			if !stateValid(p.State) {
				return false
			}
		}
	}
	// Enforce the tagged response shape for hostile/misconfigured peers too.
	switch r.Operation {
	case Snapshot:
		return v.Diagnostics != nil && v.Cache == nil && len(v.Traces) == 0 && len(v.Releases) == 0 && !v.Tested
	case CacheStatus:
		return v.Cache != nil && v.Diagnostics == nil && len(v.Traces) == 0 && len(v.Releases) == 0 && !v.Tested
	case Traces:
		return v.Cache == nil && v.Diagnostics == nil && len(v.Releases) == 0 && !v.Tested
	case ReleaseHistory, Variants:
		return v.Cache == nil && v.Diagnostics == nil && len(v.Traces) == 0 && !v.Tested
	case TestProvider, TestIndexer, TestDebrid:
		return v.Tested && v.Cache == nil && v.Diagnostics == nil && len(v.Traces) == 0 && len(v.Releases) == 0
	}
	return false
}
func writeError(w http.ResponseWriter, err error) {
	code := http.StatusServiceUnavailable
	message := "unavailable"
	switch {
	case errors.Is(err, ErrDenied):
		code = http.StatusForbidden
		message = "denied"
	case errors.Is(err, ErrInvalid):
		code = http.StatusBadRequest
		message = "invalid"
	case errors.Is(err, ErrUnsupported):
		code = http.StatusNotImplemented
		message = "unsupported"
	}
	// Dependency error text is never copied into responses or logs.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	io.WriteString(w, `{"error":"`+strings.TrimSpace(message)+`"}`)
}
