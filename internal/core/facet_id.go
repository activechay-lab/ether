package core

import (
	"crypto/sha256"
	"encoding/hex"
)

// FacetID is a facet value's stable identifier (a genre, studio or country
// as the Jellyfin and Emby surfaces name it): a version-5-shaped UUID of the
// kind and value, without dashes. The catalogue stores it beside the studio
// vocabulary so an identifier resolves by index instead of by hashing every
// stored value.
func FacetID(kind, value string) string {
	sum := sha256.Sum256([]byte("ether/facet/v1\x00" + kind + "\x00" + value))
	sum[6] = (sum[6] & 0x0f) | 0x50
	sum[8] = (sum[8] & 0x3f) | 0x80
	return hex.EncodeToString(sum[:16])
}
