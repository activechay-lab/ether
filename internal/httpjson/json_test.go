package httpjson

import (
	"net/http/httptest"
	"testing"
)

func TestEncodingFailureDoesNotCommitSuccess(t *testing.T) {
	w := httptest.NewRecorder()
	Write(w, 201, map[string]any{"bad": make(chan int)})
	if w.Code != 500 {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	Write(w, 201, map[string]string{"a": "<b>"})
	if w.Code != 201 || w.Body.String() != `{"a":"\u003cb\u003e"}`+"\n" {
		t.Fatalf("response %d %q", w.Code, w.Body.String())
	}
}
