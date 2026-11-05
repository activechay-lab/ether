package plex

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"strings"
)

// orderedUpgradeWriter leaves handshake validation and websocket framing to the
// websocket library. Only the successful HTTP/1 upgrade envelope is reordered
// to the recorded PMS header sequence (findings.md, cycle 35).
type orderedUpgradeWriter struct{ http.ResponseWriter }

func (w orderedUpgradeWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, rw, err := http.NewResponseController(w.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	return &orderedUpgradeConn{Conn: c}, rw, nil
}

type orderedUpgradeConn struct {
	net.Conn
	upgraded bool
}

func (c *orderedUpgradeConn) Write(p []byte) (int, error) {
	if c.upgraded {
		return c.Conn.Write(p)
	}
	c.upgraded = true
	if !bytes.HasPrefix(p, []byte("HTTP/1.1 101 ")) {
		return c.Conn.Write(p)
	}
	lines := strings.Split(string(p), "\r\n")
	var out strings.Builder
	out.WriteString(lines[0] + "\r\n")
	order := []string{"X-Plex-Protocol", "Access-Control-Expose-Headers", "Vary", "Upgrade", "Connection", "Sec-WebSocket-Accept", "Cache-Control", "Access-Control-Allow-Origin"}
	written := make(map[int]bool)
	write := func(name string) {
		for i, line := range lines[1:] {
			k, _, ok := strings.Cut(line, ":")
			if ok && strings.EqualFold(k, name) {
				out.WriteString(line + "\r\n")
				written[i+1] = true
			}
		}
	}
	for _, name := range order {
		write(name)
	}
	// Preserve Ether's security headers and any future extension fields.
	for i, line := range lines[1:] {
		if line != "" && !written[i+1] && !strings.HasPrefix(line, "Date:") {
			out.WriteString(line + "\r\n")
		}
	}
	write("Date")
	out.WriteString("\r\n")
	b := []byte(out.String())
	n, err := c.Conn.Write(b)
	if err != nil {
		return 0, err
	}
	if n != len(b) {
		return 0, net.ErrClosed
	}
	return len(p), nil
}
