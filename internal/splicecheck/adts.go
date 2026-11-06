package splicecheck

import (
	"crypto/sha256"
	"fmt"
)

// Hash raw AAC access units, stripping the ADTS header and optional CRC.
// Multiple raw_data_blocks and unsupported profiles are rejected, not guessed.
func adtsHashes(b []byte) ([]string, error) {
	var out []string
	for len(b) > 0 {
		if len(b) < 7 || b[0] != 255 || b[1]&0xf6 != 0xf0 || b[6]&3 != 0 {
			return nil, fmt.Errorf("unsupported/truncated ADTS")
		}
		n := int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5]>>5)
		hdr := 7
		if b[1]&1 == 0 {
			hdr = 9
		}
		if n <= hdr || n > len(b) {
			return nil, fmt.Errorf("truncated AAC AU")
		}
		out = append(out, fmt.Sprintf("%x", sha256.Sum256(b[hdr:n])))
		b = b[n:]
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no AAC access units")
	}
	return out, nil
}
