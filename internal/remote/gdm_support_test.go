package remote

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"

	"net"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"

	"time"
)

// ParseGDMReply parses an "HTTP/1.0 200 OK" GDM reply.
func ParseGDMReply(b []byte) (*GDMReply, error) {
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(b)))
	status, err := r.ReadLine()
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(status, "HTTP/1.") || !strings.Contains(status, " 200") {
		return nil, fmt.Errorf("gdm: unexpected status line %q", status)
	}
	h, err := r.ReadMIMEHeader()
	if err != nil && len(h) == 0 {
		return nil, err
	}
	rep := &GDMReply{
		Headers:           http.Header(h),
		ContentType:       h.Get("Content-Type"),
		Name:              h.Get("Name"),
		MachineIdentifier: h.Get("Resource-Identifier"),
		Version:           h.Get("Version"),
	}
	rep.Port, _ = strconv.Atoi(h.Get("Port"))
	if s, err := strconv.ParseInt(h.Get("Updated-At"), 10, 64); err == nil {
		rep.UpdatedAt = time.Unix(s, 0)
	}
	return rep, nil
}

// DiscoverGDM sends one M-SEARCH to target ("239.0.0.250:32414" when empty)
// and collects replies until ctx is done or wait elapses.
func DiscoverGDM(ctx context.Context, target string, wait time.Duration) ([]GDMReply, error) {
	if target == "" {
		target = net.JoinHostPort(GDMMulticastGroup, strconv.Itoa(GDMServerPort))
	}
	if wait <= 0 {
		wait = 2 * time.Second
	}
	raddr, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if _, err := conn.WriteToUDP([]byte("M-SEARCH * HTTP/1.1\r\n\r\n"), raddr); err != nil {
		return nil, err
	}
	var out []GDMReply
	buf := make([]byte, 2048)
	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				return out, nil
			}
			return out, err
		}
		rep, err := ParseGDMReply(buf[:n])
		if err != nil {
			continue
		}
		rep.From = src
		out = append(out, *rep)
	}
}
