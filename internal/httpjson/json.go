// Package httpjson writes JSON only after successful encoding.
package httpjson

import (
	"bytes"
	"encoding/json"
	"net/http"
)

// Write buffers encoding so an unsupported value cannot commit a success status.
func Write(w http.ResponseWriter, status int, v any) {
	var b bytes.Buffer
	if err := json.NewEncoder(&b).Encode(v); err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}
