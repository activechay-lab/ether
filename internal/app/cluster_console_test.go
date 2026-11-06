package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
)

// The read-only endpoint still contains session topology and requires an owner,
// including on the local network while first-run setup is open.
func TestClusterConsoleOwnerBoundary(t *testing.T) {
	a, err := New(context.Background(), Options{DataDir: t.TempDir(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	request := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://localhost/ether/api"+path, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("X-Ether-Request", "1")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Ether-Admin-Token", token)
		w := httptest.NewRecorder()
		a.Handler().ServeHTTP(w, req)
		return w
	}
	if got := request("GET", "/cluster/console", "", ""); got.Code != 401 {
		t.Fatalf("setup must not disclose topology: %d", got.Code)
	}
	setup := request("POST", "/setup/local", `{"username":"console-owner","password":"console-owner-test-password"}`, "")
	if setup.Code != 200 {
		t.Fatalf("owner setup %d", setup.Code)
	}
	var auth struct {
		Token string `json:"token"`
	}
	if err = json.Unmarshal(setup.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	response := request("GET", "/cluster/console", "", auth.Token)
	if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("owner status %d", response.Code)
	}
	var body struct {
		Assignments []any `json:"assignments"`
		Events      []any `json:"events"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Assignments == nil || body.Events == nil || len(body.Assignments) != 0 || len(body.Events) != 0 {
		t.Fatal("unavailable observations must be empty lists")
	}
}

func TestClusterPlaybackDisplayProjection(t *testing.T) {
	display := clusterPlaybackDisplayJSON(`{"ReadToken":"private-read","Binding":{"StreamToken":"private-stream","Decision":{"videoCodec":"h264","maxHeight":1080,"bitrateKbps":8000}},"Source":{"TitleName":"Arrival (2016)","Username":"Alex","Key":1000000000000329,"Client":{"deviceName":"Living room","product":"Plex for Apple TV"}}}`)
	if display == nil || display.Title != "Arrival (2016)" || display.Viewer != "Alex" || display.Device != "Living room" || display.BitrateKbps != 8000 {
		t.Fatalf("display facts not projected: %#v", display)
	}
	raw, err := json.Marshal(display)
	if err != nil || strings.Contains(string(raw), "private-") {
		t.Fatal("private protocol fields escaped into owner display")
	}
}
