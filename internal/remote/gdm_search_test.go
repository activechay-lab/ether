package remote

import "testing"

// The PMS search recognizer compares the bytes available, up to its fixed
// method prefix. The remainder is opaque, including the HTTP version.
func TestGDMSearchPrefix(t *testing.T) {
	const request = "M-SEARCH * HTTP/2.0\r\n\r\n"
	for n := 0; n <= len(request); n++ {
		if !isMSearch([]byte(request[:n])) {
			t.Errorf("rejected prefix of length %d", n)
		}
	}
	const prefix = "M-SEARCH * "
	for n := range len(prefix) {
		packet := []byte(request)
		packet[n] = 'X'
		if isMSearch(packet) {
			t.Errorf("accepted wrong byte at %d", n)
		}
	}
	for _, packet := range []string{" ", "\x00", "\r\n", " M-SEARCH * HTTP/1.1", "\r\nM-SEARCH * HTTP/1.1", "m-search * HTTP/1.1", "M-SEARCH\t* HTTP/1.1", "M-SEARCH *\r\n"} {
		if isMSearch([]byte(packet)) {
			t.Errorf("accepted %q", packet)
		}
	}
}
