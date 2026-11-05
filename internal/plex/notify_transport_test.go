package plex

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

func TestNotificationFilterPresenceAndLastValue(t *testing.T) {
	for _, tt := range []struct {
		query          string
		logs, timeline bool
	}{
		{"", false, true}, {"?filters=", true, true}, {"?filters=log", true, false},
		{"?filters=all", false, false}, {"?filters=timeline&filters=log", true, false},
		{"?filters=log&filters=", true, true}, {"?filters=log&filters=timeline", false, true},
	} {
		t.Run(tt.query, func(t *testing.T) {
			n := newNotifier()
			sub, _ := n.subscribe(notificationFilters(httptest.NewRequest("GET", "/"+tt.query, nil)), &core.User{ID: 1, IsOwner: true})
			for _, kind := range []string{"log", "timeline"} {
				n.deliver(newNotification(kind, []byte(`{}`)), 0)
				got := false
				select {
				case <-sub.ch:
					got = true
				default:
				}
				want := tt.logs
				if kind == "timeline" {
					want = tt.timeline
				}
				if got != want {
					t.Errorf("%s received=%v want=%v", kind, got, want)
				}
			}
		})
	}
}

func TestNotificationSlowConsumerIsolation(t *testing.T) {
	n := newNotifier()
	slow, _ := n.subscribe(nil, &core.User{ID: 1, IsOwner: true})
	friend, _ := n.subscribe([]string{"log"}, &core.User{ID: 2})
	for i := 0; i < 1000; i++ {
		n.deliver(newNotification("timeline", nil), 0)
	}
	if len(slow.ch) != cap(slow.ch) {
		t.Fatal("bounded backlog not filled")
	}
	n.deliver(newNotification("log", nil), 0)
	if len(friend.ch) != 0 {
		t.Fatal("logs leaked to shared viewer")
	}
}

func rawNotificationSocket(t *testing.T, f *fixture) (net.Conn, *bufio.Reader, []string) {
	t.Helper()
	ts := httptest.NewServer(f.srv)
	t.Cleanup(ts.Close)
	c, err := net.Dial("tcp", strings.TrimPrefix(ts.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.SetDeadline(time.Now().Add(15 * time.Second))
	fmt.Fprintf(c, "GET /:/websockets/notifications?X-Plex-Token=%s HTTP/1.1\r\nHost: localhost\r\nOrigin: https://app.plex.tv\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Extensions: permessage-deflate\r\n\r\n", ownerToken)
	br := bufio.NewReader(c)
	line, _ := br.ReadString('\n')
	if !strings.Contains(line, "101") {
		t.Fatal(line)
	}
	var names []string
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
		name, _, _ := strings.Cut(line, ":")
		names = append(names, name)
	}
	return c, br, names
}

func TestNotificationRawHandshakeAndControlFrames(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	c, br, names := rawNotificationSocket(t, f)
	expected := []string{"X-Plex-Protocol", "Access-Control-Expose-Headers", "Vary", "Upgrade", "Connection", "Sec-WebSocket-Accept", "Cache-Control", "Access-Control-Allow-Origin"}
	for i, want := range expected {
		if i >= len(names) || !strings.EqualFold(names[i], want) {
			t.Fatalf("headers %v", names)
		}
	}
	if names[len(names)-1] != "Date" {
		t.Fatalf("Date not last: %v", names)
	}
	for _, name := range names {
		if strings.EqualFold(name, "Sec-WebSocket-Extensions") {
			t.Fatal("extension echoed")
		}
	}
	// Masked client PING must be answered immediately with the same payload.
	c.Write([]byte{0x89, 0x83, 1, 2, 3, 4, 'a' ^ 1, 'b' ^ 2, 'c' ^ 3})
	h := make([]byte, 2)
	if _, err := io.ReadFull(br, h); err != nil {
		t.Fatal(err)
	}
	p := make([]byte, int(h[1]))
	io.ReadFull(br, p)
	if h[0] != 0x8a || string(p) != "abc" {
		t.Fatalf("pong %x %q", h, p)
	}
	// Receiving the pong must not trigger another ping; first scheduled ping is
	// ten seconds after connection, including clients that never pong back.
	start := time.Now()
	if _, err := io.ReadFull(br, h); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 9*time.Second {
		t.Fatalf("early ping %s", elapsed)
	}
	p = make([]byte, int(h[1]))
	io.ReadFull(br, p)
	if h[0] != 0x89 || len(p) != 1 {
		t.Fatalf("ping %x %x", h, p)
	}
	// Normal close is consumed and acknowledged, then subscriber is reaped.
	c.Write([]byte{0x88, 0x82, 1, 2, 3, 4, 3 ^ 1, 0xe8 ^ 2})
	if _, err := io.ReadFull(br, h); err != nil {
		t.Fatal(err)
	}
	p = make([]byte, int(h[1]))
	io.ReadFull(br, p)
	if h[0] != 0x88 || len(p) < 2 || p[0] != 3 || p[1] != 0xe8 {
		t.Fatalf("close %x %x", h, p)
	}
}

func TestNotificationFrameOrderAndSSEProjection(t *testing.T) {
	n, ok := encodeNotification(model.NotificationContainer{Type: "timeline", Size: 1, TimelineEntry: []model.TimelineEntry{{Identifier: model.LibraryIdentifier, SectionID: "1", ItemID: "42", Type: 1, Title: "Example (2000)", State: 5, UpdatedAt: 123}}})
	if !ok {
		t.Fatal("encode")
	}
	want := `{"NotificationContainer":{"type":"timeline","size":1,"TimelineEntry":[{"identifier":"com.plexapp.plugins.library","sectionID":"1","itemID":"42","type":1,"title":"Example (2000)","state":5,"updatedAt":123}]}}`
	if string(n.data) != want {
		t.Fatalf("frame %s", n.data)
	}
	if len(n.sse) != 1 || strings.Contains(string(n.sse[0]), "NotificationContainer") || strings.Contains(string(n.sse[0]), "metadataState") {
		t.Fatalf("SSE %s", n.sse)
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(n.sse[0], &v) != nil || len(v) != 1 || v["TimelineEntry"] == nil {
		t.Fatalf("bare entry %s", n.sse[0])
	}
}

func TestNotificationSSEOriginHeaders(t *testing.T) {
	for _, origin := range []string{"", "https://app.plex.tv"} {
		f := newFixture()
		ts := httptest.NewServer(f.srv)
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, "GET", ts.URL+"/:/eventsource/notifications?X-Plex-Token="+ownerToken, nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Header.Get("Access-Control-Allow-Origin") != origin || resp.Header.Get("Content-Type") != "text/event-stream" {
			t.Fatalf("headers %v", resp.Header)
		}
		cancel()
		resp.Body.Close()
		ts.Close()
		f.srv.Close()
	}
}
