// Package metaparse reads embedded metadata (EPUB OPF, ComicInfo.xml,
// ID3v2, FLAC Vorbis comments) into plain maps a provider can copy
// into KindItem fields. Standard library only; parsers are forgiving
// and never fail a whole item over one bad tag.
package metaparse

import (
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/braedonsaunders/ether/sdk/go/kit/archivex"
)

// EPUB parses a book's OPF metadata out of epub bytes.
func EPUB(data []byte) (map[string]any, error) {
	container, err := archivex.ReadMember(data, "META-INF/container.xml")
	if err != nil {
		return nil, err
	}
	var cont struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &cont); err != nil || len(cont.Rootfiles) == 0 {
		return nil, err
	}
	opf, err := archivex.ReadMember(data, cont.Rootfiles[0].FullPath)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Metadata struct {
			Title    string   `xml:"title"`
			Creators []string `xml:"creator"`
			Language string   `xml:"language"`
			Date     string   `xml:"date"`
			Subjects []string `xml:"subject"`
			Desc     string   `xml:"description"`
		} `xml:"metadata"`
	}
	// encoding/xml matches local names, so dc: prefixes parse flat.
	if err := xml.Unmarshal(stripPrefixes(opf), &doc); err != nil {
		return nil, err
	}
	out := map[string]any{}
	put(out, "title", doc.Metadata.Title)
	if len(doc.Metadata.Creators) > 0 {
		out["author"] = doc.Metadata.Creators
	}
	put(out, "language", doc.Metadata.Language)
	put(out, "summary", doc.Metadata.Desc)
	if len(doc.Metadata.Subjects) > 0 {
		out["subjects"] = doc.Metadata.Subjects
	}
	if y, ok := yearOf(doc.Metadata.Date); ok {
		out["year"] = y
	}
	return out, nil
}

// ComicInfo parses ComicInfo.xml (the cbz/cbr sidecar) into fields.
func ComicInfo(data []byte) (map[string]any, error) {
	var doc struct {
		Title     string `xml:"Title"`
		Series    string `xml:"Series"`
		Number    string `xml:"Number"`
		Volume    int    `xml:"Volume"`
		Year      int    `xml:"Year"`
		Summary   string `xml:"Summary"`
		Writer    string `xml:"Writer"`
		Genre     string `xml:"Genre"`
		PageCount int    `xml:"PageCount"`
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	out := map[string]any{}
	put(out, "title", doc.Title)
	put(out, "series", doc.Series)
	put(out, "number", doc.Number)
	put(out, "summary", doc.Summary)
	if doc.Writer != "" {
		out["writer"] = splitPeople(doc.Writer)
	}
	if doc.Genre != "" {
		out["genre"] = splitPeople(doc.Genre)
	}
	if doc.Volume > 0 {
		out["volume"] = doc.Volume
	}
	if doc.Year > 0 {
		out["year"] = doc.Year
	}
	if doc.PageCount > 0 {
		out["pageCount"] = doc.PageCount
	}
	return out, nil
}

// ID3 reads ID3v2.3/2.4 text frames (title, album, artist, track, year…).
func ID3(data []byte) map[string]any {
	if len(data) < 10 || string(data[:3]) != "ID3" {
		return nil
	}
	ver := data[3]
	size := syncSafe(data[6:10])
	body := data[10:]
	if size < len(body) {
		body = body[:size]
	}
	frames := map[string]string{
		"TIT2": "title", "TALB": "album", "TPE1": "artist", "TPE2": "albumArtist",
		"TRCK": "track", "TPOS": "disc", "TCON": "genre", "TYER": "year",
		"TDRC": "year", "TCOM": "composer", "TLEN": "durationMs",
	}
	out := map[string]any{}
	for len(body) >= 10 {
		id := string(body[:4])
		if id[0] == 0 {
			break
		}
		var fsize int
		if ver >= 4 {
			fsize = syncSafe(body[4:8])
		} else {
			fsize = int(binary.BigEndian.Uint32(body[4:8]))
		}
		body = body[10:]
		if fsize < 0 || fsize > len(body) {
			break
		}
		if key, ok := frames[id]; ok && fsize > 0 {
			out[key] = decodeID3Text(body[:fsize])
		}
		body = body[fsize:]
	}
	if n, ok := out["track"].(string); ok {
		if i, err := strconv.Atoi(strings.SplitN(n, "/", 2)[0]); err == nil {
			out["track"] = i
		}
	}
	if y, ok := out["year"].(string); ok {
		if yr, valid := yearOf(y); valid {
			out["year"] = yr
		}
	}
	return out
}

// FLAC reads the Vorbis comment block of a FLAC stream.
func FLAC(data []byte) map[string]any {
	if len(data) < 8 || string(data[:4]) != "fLaC" {
		return nil
	}
	rest := data[4:]
	for len(rest) >= 4 {
		last := rest[0]&0x80 != 0
		typ := rest[0] & 0x7f
		n := int(rest[1])<<16 | int(rest[2])<<8 | int(rest[3])
		rest = rest[4:]
		if n > len(rest) {
			return nil
		}
		if typ == 4 { // Vorbis comment
			return parseVorbis(rest[:n])
		}
		rest = rest[n:]
		if last {
			break
		}
	}
	return nil
}

func parseVorbis(b []byte) map[string]any {
	readStr := func() (string, bool) {
		if len(b) < 4 {
			return "", false
		}
		n := int(binary.LittleEndian.Uint32(b))
		b = b[4:]
		if n > len(b) {
			return "", false
		}
		s := string(b[:n])
		b = b[n:]
		return s, true
	}
	if _, ok := readStr(); !ok || len(b) < 4 { // vendor
		return nil
	}
	count := int(binary.LittleEndian.Uint32(b))
	b = b[4:]
	out := map[string]any{}
	for i := 0; i < count; i++ {
		s, ok := readStr()
		if !ok {
			break
		}
		k, v, found := strings.Cut(s, "=")
		if !found {
			continue
		}
		key := map[string]string{
			"TITLE": "title", "ALBUM": "album", "ARTIST": "artist", "ALBUMARTIST": "albumArtist",
			"TRACKNUMBER": "track", "DISCNUMBER": "disc", "GENRE": "genre", "DATE": "year",
		}[strings.ToUpper(k)]
		if key == "" {
			key = strings.ToLower(k)
		}
		out[key] = v
	}
	if y, ok := out["year"].(string); ok {
		if yr, valid := yearOf(y); valid {
			out["year"] = yr
		}
	}
	return out
}

func decodeID3Text(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	switch b[0] {
	case 3: // UTF-8
		return strings.TrimRight(string(b[1:]), "\x00")
	case 1: // UTF-16 with BOM
		body := b[1:]
		if len(body) >= 2 && body[0] == 0xfe && body[1] == 0xff {
			return decodeUTF16(body[2:], binary.BigEndian)
		}
		if len(body) >= 2 && body[0] == 0xff && body[1] == 0xfe {
			return decodeUTF16(body[2:], binary.LittleEndian)
		}
	case 2:
		return decodeUTF16(b[1:], binary.BigEndian)
	default: // Latin-1, close enough for tags
		runes := make([]rune, 0, len(b))
		for _, c := range b[1:] {
			runes = append(runes, rune(c))
		}
		return strings.TrimRight(string(runes), "\x00")
	}
	return ""
}

func decodeUTF16(b []byte, order binary.ByteOrder) string {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		u = append(u, order.Uint16(b[i:]))
	}
	return strings.TrimRight(string(utf16.Decode(u)), "\x00")
}

func syncSafe(b []byte) int {
	n := 0
	for _, c := range b {
		n = n<<7 | int(c&0x7f)
	}
	return n
}

// stripPrefixes removes dc:/opf: element prefixes so encoding/xml local
// matching sees plain names. Good enough for metadata reading.
func stripPrefixes(b []byte) []byte {
	for _, p := range []string{"dc:", "opf:"} {
		b = bytes.ReplaceAll(b, []byte("<"+p), []byte("<"))
		b = bytes.ReplaceAll(b, []byte("</"+p), []byte("</"))
	}
	return b
}

func put(m map[string]any, k, v string) {
	if strings.TrimSpace(v) != "" {
		m[k] = strings.TrimSpace(v)
	}
}

func splitPeople(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func yearOf(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 4 {
		return 0, false
	}
	y, err := strconv.Atoi(s[:4])
	return y, err == nil && y > 0
}
