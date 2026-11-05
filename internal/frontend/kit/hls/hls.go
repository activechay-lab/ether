// Package hls writes token-free playlists under random session paths.
package hls

import (
	"errors"
)

var ErrInvalid = errors.New("invalid HLS playlist")
