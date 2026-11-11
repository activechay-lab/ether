package newznab

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode/utf16"
)

// nzbSniffWindow is how far into a download the <nzb root is looked for.
// Indexers put an XML declaration, a DOCTYPE and sometimes long comment
// headers (release notes, uploader info) before it.
const nzbSniffWindow = 64 << 10

// unwrapNZB turns an indexer download into NZB XML. Indexers serve the
// file gzipped (.nzb.gz, a stored body Go's transport does not undo), in a
// zip archive, as UTF-16 with a byte-order mark, or plain with a UTF-8 BOM.
// ok is false when the payload holds no NZB; kind then says, without
// echoing the body (an error page can carry the API key), what came back.
func unwrapNZB(body []byte, limit int64) (nzb []byte, kind string, ok bool, err error) {
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		gz, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, "", false, fmt.Errorf("newznab: gunzip NZB: %w", err)
		}
		body, err = io.ReadAll(io.LimitReader(gz, limit+1))
		gz.Close()
		if err != nil {
			return nil, "", false, err
		}
		if int64(len(body)) > limit {
			return nil, "", false, fmt.Errorf("newznab: NZB exceeds %d bytes", limit)
		}
	}
	if len(body) >= 4 && bytes.Equal(body[:4], []byte("PK\x03\x04")) {
		zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
		if err != nil {
			return nil, "a damaged zip archive", false, nil
		}
		var pick *zip.File
		for _, f := range zr.File {
			if strings.EqualFold(path.Ext(f.Name), ".nzb") {
				pick = f
				break
			}
		}
		if pick == nil && len(zr.File) == 1 {
			pick = zr.File[0]
		}
		if pick == nil {
			return nil, "a zip archive without an .nzb file", false, nil
		}
		rc, err := pick.Open()
		if err != nil {
			return nil, "a damaged zip archive", false, nil
		}
		body, err = io.ReadAll(io.LimitReader(rc, limit+1))
		rc.Close()
		if err != nil {
			return nil, "", false, err
		}
		if int64(len(body)) > limit {
			return nil, "", false, fmt.Errorf("newznab: NZB exceeds %d bytes", limit)
		}
	}
	body = utf8FromBOM(body)
	trimmed := bytes.TrimSpace(body)
	if bytes.Contains(bytes.ToLower(trimmed[:min(len(trimmed), nzbSniffWindow)]), []byte("<nzb")) {
		return body, "", true, nil
	}
	return nil, describePayload(trimmed), false, nil
}

// utf8FromBOM re-encodes a UTF-16 body to UTF-8 and drops a UTF-8 BOM.
func utf8FromBOM(b []byte) []byte {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:]
	case len(b) >= 2 && (b[0] == 0xFF && b[1] == 0xFE || b[0] == 0xFE && b[1] == 0xFF):
		order := binary.ByteOrder(binary.LittleEndian)
		if b[0] == 0xFE {
			order = binary.BigEndian
		}
		b = b[2:]
		units := make([]uint16, len(b)/2)
		for i := range units {
			units[i] = order.Uint16(b[2*i:])
		}
		return []byte(string(utf16.Decode(units)))
	}
	return b
}

// describePayload names what a non-NZB download was, for the owner, from
// its shape only.
func describePayload(b []byte) string {
	if len(b) == 0 {
		return "an empty response"
	}
	head := bytes.ToLower(b[:min(len(b), 512)])
	switch {
	case bytes.Contains(head, []byte("<html")) || bytes.Contains(head, []byte("<!doctype html")):
		if bytes.Contains(bytes.ToLower(b[:min(len(b), 8192)]), []byte("cloudflare")) {
			return fmt.Sprintf("a Cloudflare page (%d bytes)", len(b))
		}
		return fmt.Sprintf("an HTML page (%d bytes)", len(b))
	case b[0] == '{' || b[0] == '[':
		return fmt.Sprintf("JSON (%d bytes)", len(b))
	case b[0] == '<':
		return fmt.Sprintf("XML without an <nzb> root (%d bytes)", len(b))
	}
	return fmt.Sprintf("%s (%d bytes)", http.DetectContentType(b), len(b))
}
