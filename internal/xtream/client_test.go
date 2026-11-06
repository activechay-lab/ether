package xtream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestProtocolCredentialsNeverEscapeErrors(t *testing.T) {
	c := Client{Base: "http://panel.example", Username: "private-user", Password: "private-pass", Open: func(context.Context, string) (io.ReadCloser, error) {
		return nil, errors.New("http://panel.example/movie/private-user/private-pass/1.mp4")
	}}
	var info VODInfo
	err := c.Get(t.Context(), "get_vod_info", nil, &info)
	if err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatalf("unsafe transport error: %v", err)
	}
	c.Open = func(context.Context, string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(`{"info":"private-pass"}`)), nil
	}
	err = c.Get(t.Context(), "get_vod_info", nil, &info)
	if err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatalf("unsafe document error: %v", err)
	}
	c.Open = func(context.Context, string) (io.ReadCloser, error) { return nil, ErrBusy }
	if err = c.Get(t.Context(), "get_vod_info", nil, &info); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy classification: %v", err)
	}
	for _, doc := range []string{`"123"`, `123`, `null`, `false`, `[]`} {
		var v Value
		if e := json.Unmarshal([]byte(doc), &v); e != nil {
			t.Fatal(e)
		}
	}
	if _, err = c.StreamURL("movie", "../bad", "mp4"); err == nil {
		t.Fatal("path injection accepted")
	}
	if _, err = c.StreamURL("movie", "1", "../mp4"); err == nil {
		t.Fatal("extension injection accepted")
	}
}
