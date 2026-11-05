package model

import (
	"io"
)

// Encode writes root to w in the given format. It encodes the full document
// before writing to w so an encoding error never leaves partial output on w.
func Encode(w io.Writer, root any, f Format) error {
	b, err := Marshal(root, f)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
