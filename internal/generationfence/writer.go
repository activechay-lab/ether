// Package generationfence checks execution authority at the output consumer.
package generationfence

import (
	"errors"
	"net/http"
)

var ErrStale = errors.New("execution generation is no longer authoritative")

// Writer rechecks before headers and every body write, including after a blocked
// producer returns. Check must consult current authority, never cached membership.
// An already admitted socket write cannot be recalled; subsequent writes fail.
// It deliberately exposes no ReaderFrom or Unwrap bypass around the check.
type Writer struct {
	http.ResponseWriter
	Check             func() error
	committed, denied bool
}

func (w *Writer) valid() bool {
	if w.denied {
		return false
	}
	if w.Check() == nil {
		return true
	}
	w.denied = true
	if !w.committed {
		for k := range w.Header() {
			delete(w.Header(), k)
		}
		http.Error(w.ResponseWriter, "execution unavailable", http.StatusConflict)
		w.committed = true
	}
	return false
}
func (w *Writer) WriteHeader(status int) {
	if w.committed || !w.valid() {
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *Writer) Write(b []byte) (int, error) {
	if !w.valid() {
		return 0, ErrStale
	}
	if !w.committed {
		w.committed = true
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func (w *Writer) Flush() {
	if !w.valid() {
		return
	}
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
