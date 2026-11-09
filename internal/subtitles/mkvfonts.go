package subtitles

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

const (
	// MaxFontAttachments bounds the number of font attachments inspected or
	// returned from a single Matroska container.
	MaxFontAttachments = 32

	// MaxUnfilteredFontBytes (48 MiB) is the maximum total attachment payload
	// returned without filtering. Packs at or below this bound return all font
	// attachments in full; larger packs select only the whole font attachment
	// files whose SFNT family names (or fallback filenames) are referenced by
	// the ASS script's styles or inline \fn overrides.
	// Note: This performs attachment-level selection of whole font files, not
	// glyph-level font subsetting.
	MaxUnfilteredFontBytes = 48 << 20

	// MaxFontScanBytes (512 MiB) bounds the total raw attachment payload an
	// oversized pack may scan when selecting referenced font attachments.
	MaxFontScanBytes = 512 << 20

	// MaxASSScriptBytes (8 MiB) bounds the ASS/SSA script text parsed for font
	// family references.
	MaxASSScriptBytes = 8 << 20

	maxFontFamilyLen       = 256
	maxFontFamilies        = 128
	maxSFNTNameTableBytes  = 64 << 10
	maxSFNTNameRecords     = 1024
	maxSFNTTables          = 128
	maxSFNTCollections     = 8
	fontHeaderPrefixBytes  = 256 << 10
	maxMKVSegmentElements  = 262144
	maxMKVClusterElements  = 262144
	directClusterReadBytes = 1 << 20
	mkvAttachmentsID       = 0x1941a469
	mkvAttachedFileID      = 0x61a7
	mkvFileDescriptionID   = 0x467e
	mkvFileNameID          = 0x466e
	mkvFileMimeTypeID      = 0x4660
	mkvFileDataID          = 0x465c
	mkvFileUIDID           = 0x46ae
	mkvCodecPrivateID      = 0x63a2
	mkvDefaultDurationID   = 0x23e383
	subtitleFontTTFMIME    = "font/ttf"
	subtitleFontOTFMIME    = "font/otf"
	subtitleFontColMIME    = "font/collection"
	subtitleFontWOFFMIME   = "font/woff"
	subtitleFontWOFF2MIME  = "font/woff2"
	subtitleFontOctetMIME  = "application/octet-stream"
)

// ErrFontPackExceedsLimit reports that the attached font payload (or the
// script-referenced attachment selection) exceeds the retained byte ceiling.
var ErrFontPackExceedsLimit = errors.New("subtitle font attachments exceed size limit")

// ErrFontAttachmentSelectionUnavailable reports that an oversized font pack
// could not be narrowed to script-referenced font attachments.
var ErrFontAttachmentSelectionUnavailable = errors.New("subtitle font attachment selection unavailable")

// ErrASSTrackIncomplete reports that an embedded Matroska ASS/SSA subtitle
// track is truncated, corrupt, or exceeds the bounded script ceiling.
var ErrASSTrackIncomplete = errors.New("matroska ASS subtitle track incomplete or truncated")

// FontAttachment is a single font file attached to a Matroska container for
// ASS/SSA subtitle rendering. Name is always a sanitized basename and must
// never be used as a filesystem path or cross-title cache key.
type FontAttachment struct {
	Index    int
	Name     string
	MIMEType string
	Data     []byte
}

// FontBundleItem is the JSON-serializable representation of an attached font
// sent to web and Silo clients.
type FontBundleItem struct {
	Name string `json:"name"`
	Data string `json:"data"`
}

// EncodeFontBundle converts extracted font attachments into base64 JSON items.
func EncodeFontBundle(fonts []FontAttachment) []FontBundleItem {
	items := make([]FontBundleItem, 0, len(fonts))
	for _, f := range fonts {
		items = append(items, FontBundleItem{
			Name: f.Name,
			Data: base64.StdEncoding.EncodeToString(f.Data),
		})
	}
	return items
}

// IsASSCodec reports whether a stream codec identifier names an ASS or SSA
// text subtitle track.
func IsASSCodec(codec string) bool {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "ass", "ssa", "s_text/ass", "s_text/ssa", "text/x-ssa", "text/x-ass":
		return true
	default:
		return false
	}
}

// IsFontAttachmentMeta reports whether an attachment filename or MIME type
// identifies a subtitle font file.
func IsFontAttachmentMeta(fileName, mimeType string) bool {
	ext := strings.ToLower(filepath.Ext(cleanAttachmentBasename(fileName)))
	if isSupportedFontExt(ext) {
		return true
	}
	mime := strings.ToLower(strings.TrimSpace(mimeType))
	return strings.Contains(mime, "font") ||
		strings.Contains(mime, "truetype") ||
		strings.Contains(mime, "opentype") ||
		strings.Contains(mime, "woff")
}

func isSupportedFontExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".ttf", ".otf", ".ttc", ".otc", ".woff", ".woff2":
		return true
	default:
		return false
	}
}

func fontExtFromMIME(mimeType string) string {
	mime := strings.ToLower(strings.TrimSpace(mimeType))
	switch {
	case strings.Contains(mime, "woff2"):
		return ".woff2"
	case strings.Contains(mime, "woff"):
		return ".woff"
	case strings.Contains(mime, "collection"):
		return ".ttc"
	case strings.Contains(mime, "opentype") || strings.Contains(mime, "otf"):
		return ".otf"
	case strings.Contains(mime, "truetype") || strings.Contains(mime, "ttf") || strings.Contains(mime, "font"):
		return ".ttf"
	default:
		return ".ttf"
	}
}

func cleanAttachmentBasename(raw string) string {
	replaced := strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/")
	if idx := strings.LastIndexByte(replaced, '/'); idx >= 0 {
		replaced = replaced[idx+1:]
	}
	replaced = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' || r == ':' {
			return -1
		}
		return r
	}, replaced)
	replaced = strings.TrimSpace(replaced)
	if replaced == "" || replaced == "." || replaced == ".." || strings.HasPrefix(replaced, ".") {
		return ""
	}
	return replaced
}

// SanitizeFontAttachmentName returns a safe display filename for an untrusted
// container attachment. It strips directory components, rejects traversal or
// control characters, and guarantees a supported font extension so an
// attachment name can never become a filesystem path.
func SanitizeFontAttachmentName(rawName, mimeType string, index int) string {
	fallback := fmt.Sprintf("attachment-%d%s", max(index, 0), fontExtFromMIME(mimeType))
	base := cleanAttachmentBasename(rawName)
	if base == "" || len(base) > maxFontFamilyLen {
		return fallback
	}
	ext := strings.ToLower(filepath.Ext(base))
	if !isSupportedFontExt(ext) {
		return fallback
	}
	return base
}

// FontAttachmentMIMEType returns a safe, static MIME type derived strictly
// from the sanitized font extension rather than trusting container metadata.
func FontAttachmentMIMEType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf":
		return subtitleFontTTFMIME
	case ".otf":
		return subtitleFontOTFMIME
	case ".ttc", ".otc":
		return subtitleFontColMIME
	case ".woff":
		return subtitleFontWOFFMIME
	case ".woff2":
		return subtitleFontWOFF2MIME
	default:
		return subtitleFontOctetMIME
	}
}

// ParseASSFontFamilies extracts the distinct font family names referenced by
// an ASS/SSA script: the Fontname column of every Style line under
// [V4+ Styles] / [V4 Styles] (honoring the section's Format line) plus every
// inline {\fn...} override tag in dialogue events.
func ParseASSFontFamilies(script []byte) []string {
	if len(script) > MaxASSScriptBytes {
		script = script[:MaxASSScriptBytes]
	}
	var families []string
	seen := make(map[string]struct{})
	add := func(name string) {
		name = strings.TrimSpace(name)
		name = strings.TrimPrefix(name, "@")
		name = strings.TrimSpace(name)
		if name == "" || len(name) > maxFontFamilyLen {
			return
		}
		key := strings.ToLower(name)
		if _, ok := seen[key]; ok {
			return
		}
		if len(families) >= maxFontFamilies {
			return
		}
		seen[key] = struct{}{}
		families = append(families, name)
	}

	inStyles := false
	fontnameIdx := -1
	for _, raw := range strings.Split(string(script), "\n") {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			lower := strings.ToLower(line)
			inStyles = strings.HasPrefix(lower, "[v4") && strings.Contains(lower, "styles")
			fontnameIdx = -1
			if inStyles {
				fontnameIdx = 1
			}
			continue
		}
		if inStyles {
			switch {
			case hasASSRecordPrefix(line, "Format:"):
				fields := strings.Split(line[len("Format:"):], ",")
				for i, field := range fields {
					if strings.EqualFold(strings.TrimSpace(field), "Fontname") {
						fontnameIdx = i
						break
					}
				}
			case hasASSRecordPrefix(line, "Style:"):
				fields := strings.Split(line[len("Style:"):], ",")
				if fontnameIdx >= 0 && fontnameIdx < len(fields) {
					add(fields[fontnameIdx])
				}
			}
		}
		for _, fn := range assInlineFontOverrides(line) {
			add(fn)
		}
	}
	return families
}

func hasASSRecordPrefix(line, prefix string) bool {
	return len(line) >= len(prefix) && strings.EqualFold(line[:len(prefix)], prefix)
}

func assInlineFontOverrides(line string) []string {
	var families []string
	lower := strings.ToLower(line)
	for offset := 0; offset < len(lower); {
		idx := strings.Index(lower[offset:], `\fn`)
		if idx < 0 {
			break
		}
		start := offset + idx + len(`\fn`)
		end := start
		for end < len(line) && line[end] != '\\' && line[end] != '}' {
			end++
		}
		if name := strings.TrimSpace(line[start:end]); name != "" {
			families = append(families, name)
		}
		offset = end
	}
	return families
}

// ShiftASS shifts the Start and End timestamps of every Dialogue event in an
// authored ASS/SSA script by offset while preserving all headers, styles,
// layers, margins, and inline override tags.
func ShiftASS(raw []byte, offset time.Duration) []byte {
	if offset == 0 || len(raw) == 0 {
		return raw
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	var out strings.Builder
	out.Grow(len(raw))
	inEvents := false
	startIdx, endIdx, fieldCount := 1, 2, 10
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inEvents = strings.EqualFold(trimmed, "[Events]")
			if inEvents {
				startIdx, endIdx, fieldCount = 1, 2, 10
			}
		} else if inEvents && hasASSRecordPrefix(trimmed, "Format:") {
			fields := strings.Split(trimmed[len("Format:"):], ",")
			if len(fields) >= 3 {
				fieldCount = len(fields)
				for idx, f := range fields {
					switch strings.ToLower(strings.TrimSpace(f)) {
					case "start":
						startIdx = idx
					case "end":
						endIdx = idx
					}
				}
			}
		} else if inEvents && hasASSRecordPrefix(trimmed, "Dialogue:") && fieldCount >= 3 {
			rest := strings.TrimLeft(trimmed[len("Dialogue:"):], " \t")
			parts := strings.SplitN(rest, ",", fieldCount)
			if len(parts) == fieldCount && startIdx < len(parts) && endIdx < len(parts) {
				s := assTime(parts[startIdx])
				e := assTime(parts[endIdx])
				if s >= 0 && e > s {
					s += offset
					e += offset
					if e > 0 {
						if s < 0 {
							s = 0
						}
						parts[startIdx] = assTS(s)
						parts[endIdx] = assTS(e)
						line = "Dialogue: " + strings.Join(parts, ",")
					}
				}
			}
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	return []byte(out.String())
}

// EmbeddedFontFamilyNames parses SFNT (TrueType/OpenType and TTC/OTC
// collection) name tables from a bounded byte slice and returns the family,
// typographic family, full name, and PostScript name records. Untrusted font
// bytes are strictly bounds-checked and never panic or allocate unboundedly.
func EmbeddedFontFamilyNames(data []byte) []string {
	if len(data) < 12 {
		return nil
	}
	if string(data[:4]) == "ttcf" {
		numFonts := int(sfntBE32(data[8:12]))
		if numFonts <= 0 || numFonts > maxSFNTCollections {
			return nil
		}
		var names []string
		for i := 0; i < numFonts; i++ {
			offset := int(sfntBE32At(data, 12+4*i))
			if offset <= 0 || offset >= len(data) {
				continue
			}
			names = append(names, sfntFontNamesAt(data, offset)...)
		}
		return names
	}
	return sfntFontNamesAt(data, 0)
}

func sfntFontNamesAt(data []byte, base int) []string {
	if base < 0 || base+12 > len(data) {
		return nil
	}
	header := data[base:]
	switch string(header[:4]) {
	case "\x00\x01\x00\x00", "OTTO", "true", "typ1":
	default:
		return nil
	}
	numTables := int(sfntBE16(header[4:6]))
	if numTables <= 0 || numTables > maxSFNTTables {
		return nil
	}
	if len(header) < 12+numTables*16 {
		return nil
	}
	nameOffset, nameLength := -1, 0
	for i := 0; i < numTables; i++ {
		rec := header[12+i*16:]
		if string(rec[:4]) != "name" {
			continue
		}
		nameOffset = int(sfntBE32(rec[8:12]))
		nameLength = int(sfntBE32(rec[12:16]))
		break
	}
	if nameOffset < 0 || nameLength <= 0 || nameLength > maxSFNTNameTableBytes {
		return nil
	}
	if nameOffset > len(data) || nameLength > len(data)-nameOffset {
		return nil
	}
	table := data[nameOffset : nameOffset+nameLength]
	if len(table) < 6 {
		return nil
	}
	count := int(sfntBE16(table[2:4]))
	stringOffset := int(sfntBE16(table[4:6]))
	if count <= 0 || count > maxSFNTNameRecords {
		return nil
	}
	if len(table) < 6+count*12 {
		return nil
	}
	priority := []int{16, 1, 4, 6}
	var names []string
	seen := make(map[string]struct{})
	for _, wanted := range priority {
		for i := 0; i < count; i++ {
			rec := table[6+i*12:]
			platform := sfntBE16(rec[0:2])
			nameID := int(sfntBE16(rec[6:8]))
			if nameID != wanted {
				continue
			}
			length := int(sfntBE16(rec[8:10]))
			offset := int(sfntBE16(rec[10:12]))
			start := stringOffset + offset
			if length <= 0 || start < 0 || start > len(table) || length > len(table)-start {
				continue
			}
			decoded := decodeSFNTNameRecord(platform, table[start:start+length])
			decoded = strings.TrimSpace(decoded)
			if decoded == "" || len(decoded) > maxFontFamilyLen {
				continue
			}
			key := strings.ToLower(decoded)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			names = append(names, decoded)
			if len(names) >= 8 {
				return names
			}
		}
	}
	return names
}

func decodeSFNTNameRecord(platform uint16, raw []byte) string {
	if platform == 3 || platform == 0 {
		if len(raw)%2 != 0 {
			raw = raw[:len(raw)-1]
		}
		runes := make([]rune, 0, len(raw)/2)
		for i := 0; i+1 < len(raw); i += 2 {
			runes = append(runes, rune(binary.BigEndian.Uint16(raw[i:i+2])))
		}
		return string(runes)
	}
	return string(raw)
}

func sfntBE16(b []byte) uint16 {
	if len(b) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(b[:2])
}

func sfntBE32(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	return binary.BigEndian.Uint32(b[:4])
}

func sfntBE32At(b []byte, off int) uint32 {
	if off < 0 || off+4 > len(b) {
		return 0
	}
	return binary.BigEndian.Uint32(b[off : off+4])
}

func normalizeFontName(name string) string {
	name = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(name), "@"))
	var b strings.Builder
	lastSpace := true
	for _, r := range strings.ToLower(name) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+':
			b.WriteRune(r)
			lastSpace = false
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '.':
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func fontNameMatchesReferenced(candidate string, referenced []string) bool {
	candNorm := normalizeFontName(candidate)
	if candNorm == "" {
		return false
	}
	candTokens := strings.Fields(candNorm)
	candCompact := strings.ReplaceAll(candNorm, " ", "")
	for _, ref := range referenced {
		if ref == candNorm {
			return true
		}
		refTokens := strings.Fields(ref)
		if fontTokenSubset(refTokens, candTokens) || fontTokenSubset(candTokens, refTokens) {
			return true
		}
		refCompact := strings.ReplaceAll(ref, " ", "")
		if len(refCompact) >= 3 && len(candCompact) >= 3 &&
			(strings.Contains(candCompact, refCompact) || strings.Contains(refCompact, candCompact)) {
			return true
		}
	}
	return false
}

func fontTokenSubset(small, large []string) bool {
	if len(small) == 0 || len(small) > len(large) {
		return false
	}
	for _, tok := range small {
		if !slices.Contains(large, tok) {
			return false
		}
	}
	return true
}

func fontAttachmentMatchesReferenced(rawName string, fontData []byte, referencedNorm []string) bool {
	for _, embedded := range EmbeddedFontFamilyNames(fontData) {
		if fontNameMatchesReferenced(embedded, referencedNorm) {
			return true
		}
	}
	base := cleanAttachmentBasename(rawName)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	return stem != "" && fontNameMatchesReferenced(stem, referencedNorm)
}

// SelectReferencedFontAttachments returns all fonts when their total byte size
// is at or below maxBytes (48 MiB). When the pack exceeds maxBytes, it selects
// only the whole font attachments whose SFNT family names (or filename stems)
// match the font families referenced by assScript. This is attachment-level
// selection of required font files, not glyph-level font subsetting.
func SelectReferencedFontAttachments(fonts []FontAttachment, assScript []byte, maxBytes int64) ([]FontAttachment, error) {
	if maxBytes <= 0 {
		maxBytes = MaxUnfilteredFontBytes
	}
	if len(fonts) > MaxFontAttachments {
		fonts = fonts[:MaxFontAttachments]
	}
	var total int64
	for _, f := range fonts {
		total += int64(len(f.Data))
	}
	if total <= maxBytes {
		return fonts, nil
	}
	if total > MaxFontScanBytes {
		return nil, fmt.Errorf("%w: %d bytes exceeds scan limit", ErrFontPackExceedsLimit, total)
	}
	rawFamilies := ParseASSFontFamilies(assScript)
	var referencedNorm []string
	for _, fam := range rawFamilies {
		if norm := normalizeFontName(fam); norm != "" {
			referencedNorm = append(referencedNorm, norm)
		}
	}
	if len(referencedNorm) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrFontAttachmentSelectionUnavailable, ErrFontPackExceedsLimit)
	}
	var selected []FontAttachment
	var keptBytes int64
	for _, f := range fonts {
		if !fontAttachmentMatchesReferenced(f.Name, f.Data, referencedNorm) {
			continue
		}
		sz := int64(len(f.Data))
		if sz > maxBytes-keptBytes {
			return nil, fmt.Errorf("%w: %w", ErrFontAttachmentSelectionUnavailable, ErrFontPackExceedsLimit)
		}
		selected = append(selected, f)
		keptBytes += sz
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrFontAttachmentSelectionUnavailable, ErrFontPackExceedsLimit)
	}
	return selected, nil
}

type mkvFontSlot struct {
	index    int
	rawName  string
	mimeType string
	dataOff  int64
	dataLen  int64
}

type mkvASSTrack struct {
	trackNum    uint64
	codecPriv   []byte
	defaultDur  uint64
	streamIndex int
}

// ReadMKVFontAttachments reads embedded font attachments from a Matroska
// container using bounded EBML range reads. If the total attached font bytes
// exceed MaxUnfilteredFontBytes (48 MiB), only the font attachments referenced
// by assScript (or by the container's embedded ASS/SSA tracks) are retained.
func ReadMKVFontAttachments(ctx context.Context, src io.ReaderAt, size int64, assScript []byte) ([]FontAttachment, error) {
	if src == nil || size < 16 {
		return nil, errMKV
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r := newRangeReader(ctx, src, size)
	r.maxReads, r.maxReadBytes = 32768, MaxFontScanBytes
	head, err := r.read(0, 8)
	if err != nil {
		return nil, err
	}
	if len(head) < 4 {
		return nil, errMKV
	}
	if binary.BigEndian.Uint32(head) != mkvEBMLID {
		return nil, nil
	}
	seg, err := r.findMKVSegment(size)
	if err != nil {
		return nil, err
	}
	attachmentsElem, tracksElem, err := r.findMKVAttachmentsAndTracks(seg)
	if err != nil {
		return nil, err
	}
	if attachmentsElem.id == 0 {
		return nil, nil
	}
	slots, totalBytes, err := r.scanMKVFontSlots(attachmentsElem)
	if err != nil {
		return nil, err
	}
	if len(slots) == 0 {
		return nil, nil
	}
	if totalBytes <= MaxUnfilteredFontBytes {
		out := make([]FontAttachment, 0, len(slots))
		for _, slot := range slots {
			data, err := r.readFontBytes(slot.dataOff, int(slot.dataLen))
			if err != nil {
				return nil, err
			}
			safeName := SanitizeFontAttachmentName(slot.rawName, slot.mimeType, slot.index)
			out = append(out, FontAttachment{
				Index:    slot.index,
				Name:     safeName,
				MIMEType: FontAttachmentMIMEType(safeName),
				Data:     data,
			})
		}
		return out, nil
	}

	// Oversized pack (> 48 MiB): select only font attachments referenced by
	// a complete ASS script without loading unreferenced font payloads into memory.
	combinedScript := assScript
	if len(combinedScript) == 0 && tracksElem.id != 0 {
		extractedScript, scriptErr := r.readMKVASSHeadersAndEvents(seg, tracksElem)
		if scriptErr != nil {
			return nil, fmt.Errorf("%w: %w: %w", ErrFontAttachmentSelectionUnavailable, ErrFontPackExceedsLimit, scriptErr)
		}
		combinedScript = extractedScript
	}
	rawFamilies := ParseASSFontFamilies(combinedScript)
	var referencedNorm []string
	for _, fam := range rawFamilies {
		if norm := normalizeFontName(fam); norm != "" {
			referencedNorm = append(referencedNorm, norm)
		}
	}
	if len(referencedNorm) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrFontAttachmentSelectionUnavailable, ErrFontPackExceedsLimit)
	}
	var selected []FontAttachment
	var keptBytes int64
	for _, slot := range slots {
		prefixLen := int(min(slot.dataLen, int64(fontHeaderPrefixBytes)))
		prefix, err := r.readFontBytes(slot.dataOff, prefixLen)
		if err != nil {
			return nil, err
		}
		if !fontAttachmentMatchesReferenced(slot.rawName, prefix, referencedNorm) {
			continue
		}
		if slot.dataLen > MaxUnfilteredFontBytes-keptBytes {
			return nil, fmt.Errorf("%w: %w", ErrFontAttachmentSelectionUnavailable, ErrFontPackExceedsLimit)
		}
		var fullData []byte
		if int64(len(prefix)) == slot.dataLen {
			fullData = prefix
		} else {
			fullData, err = r.readFontBytes(slot.dataOff, int(slot.dataLen))
			if err != nil {
				return nil, err
			}
		}
		safeName := SanitizeFontAttachmentName(slot.rawName, slot.mimeType, slot.index)
		selected = append(selected, FontAttachment{
			Index:    slot.index,
			Name:     safeName,
			MIMEType: FontAttachmentMIMEType(safeName),
			Data:     fullData,
		})
		keptBytes += slot.dataLen
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%w: %w", ErrFontAttachmentSelectionUnavailable, ErrFontPackExceedsLimit)
	}
	return selected, nil
}

// ReadMKVEmbeddedASS extracts the complete authored ASS/SSA script (header
// CodecPrivate and dialogue events) for a Matroska subtitle track identified
// strictly by its 0-based stream index (or all ASS subtitle tracks when
// streamIndex < 0). Any truncated cluster, read error, or script cap overflow
// returns an error rather than partial dialogue.
func ReadMKVEmbeddedASS(ctx context.Context, src io.ReaderAt, size int64, streamIndex int) (data []byte, resultErr error) {
	defer func() {
		if errors.Is(resultErr, errMKV) || errors.Is(resultErr, io.EOF) || errors.Is(resultErr, io.ErrUnexpectedEOF) {
			resultErr = fmt.Errorf("%w: %w", ErrASSTrackIncomplete, resultErr)
		}
	}()
	if src == nil || size < 16 {
		return nil, errMKV
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r := newRangeReader(ctx, src, size)
	r.maxReads, r.maxReadBytes = 32768, MaxFontScanBytes
	head, err := r.read(0, 8)
	if err != nil {
		return nil, err
	}
	if len(head) < 4 || binary.BigEndian.Uint32(head) != mkvEBMLID {
		return nil, errMKV
	}
	seg, err := r.findMKVSegment(size)
	if err != nil {
		return nil, err
	}
	_, tracksElem, err := r.findMKVAttachmentsAndTracks(seg)
	if err != nil {
		return nil, err
	}
	if tracksElem.id == 0 {
		return nil, errMKV
	}
	return r.readMKVTrackASS(seg, tracksElem, streamIndex)
}

func (r *rangeReader) readFontBytes(off int64, n int) ([]byte, error) {
	if r.segs >= rangeMaxSegments {
		r.segs = rangeMaxSegments - 1
	}
	b, err := r.read(off, n)
	if err != nil {
		return nil, err
	}
	if len(b) < n {
		return nil, io.ErrUnexpectedEOF
	}
	return b, nil
}

func (r *rangeReader) mkvFontElement(off, end int64) (mkvElem, error) {
	if r.segs >= rangeMaxSegments {
		r.segs = rangeMaxSegments - 1
	}
	return r.mkvElement(off, end)
}

func (r *rangeReader) findMKVSegment(size int64) (mkvElem, error) {
	for off, n := int64(0), 0; off < size && n < 32; n++ {
		e, err := r.mkvFontElement(off, size)
		if err != nil {
			return mkvElem{}, err
		}
		if e.id == mkvSegmentID {
			return e, nil
		}
		off = e.end
	}
	return mkvElem{}, errMKV
}

func (r *rangeReader) findMKVAttachmentsAndTracks(seg mkvElem) (attachments mkvElem, tracks mkvElem, err error) {
	targets := map[uint64]int64{}
	visited := map[int64]bool{}
	parseSeekHead := func(e mkvElem) {
		if e.end-e.start > 1<<20 {
			return
		}
		b, readErr := r.readFontBytes(e.start, int(e.end-e.start))
		if readErr != nil {
			return
		}
		children, childErr := mkvChildren(b)
		if childErr != nil {
			return
		}
		for _, c := range children {
			if c.id != mkvSeekID {
				continue
			}
			fs, _ := mkvChildren(c.b)
			var id, pos uint64
			for _, f := range fs {
				if f.id == mkvSeekIDID {
					id = mkvUint(f.b)
				}
				if f.id == mkvSeekPositionID {
					pos = mkvUint(f.b)
				}
			}
			if pos < uint64(seg.end-seg.start) {
				targets[id] = seg.start + int64(pos)
			}
		}
	}

	for off, n := seg.start, 0; off < seg.end && n < 64; n++ {
		e, elemErr := r.mkvFontElement(off, seg.end)
		if elemErr != nil {
			break
		}
		if e.id == mkvClusterID {
			break
		}
		visited[off] = true
		switch e.id {
		case mkvSeekHeadID:
			parseSeekHead(e)
		case mkvAttachmentsID:
			attachments = e
		case mkvTracksID:
			tracks = e
		}
		if attachments.id != 0 && tracks.id != 0 {
			return attachments, tracks, nil
		}
		if e.end <= off {
			break
		}
		off = e.end
	}

	if seekOff, ok := targets[mkvSeekHeadID]; ok && !visited[seekOff] {
		visited[seekOff] = true
		if e, elemErr := r.mkvFontElement(seekOff, seg.end); elemErr == nil && e.id == mkvSeekHeadID {
			parseSeekHead(e)
		}
	}
	if attachments.id == 0 {
		if attOff, ok := targets[mkvAttachmentsID]; ok && !visited[attOff] {
			if e, elemErr := r.mkvFontElement(attOff, seg.end); elemErr == nil && e.id == mkvAttachmentsID {
				attachments = e
			}
		}
	}
	if tracks.id == 0 {
		if trkOff, ok := targets[mkvTracksID]; ok && !visited[trkOff] {
			if e, elemErr := r.mkvFontElement(trkOff, seg.end); elemErr == nil && e.id == mkvTracksID {
				tracks = e
			}
		}
	}
	return attachments, tracks, nil
}

func (r *rangeReader) scanMKVFontSlots(attachments mkvElem) ([]mkvFontSlot, int64, error) {
	var slots []mkvFontSlot
	var totalBytes int64
	fontIdx := 0
	for off, count := attachments.start, 0; off < attachments.end && count < 256; count++ {
		e, err := r.mkvFontElement(off, attachments.end)
		if err != nil {
			return nil, 0, err
		}
		if e.end <= off {
			return nil, 0, errMKV
		}
		if e.id == mkvAttachedFileID && len(slots) < MaxFontAttachments {
			slot, ok, err := r.inspectAttachedFile(e, fontIdx)
			if err != nil {
				return nil, 0, err
			}
			if ok {
				if slot.dataLen <= 0 || slot.dataLen > MaxFontScanBytes-totalBytes {
					return nil, 0, ErrFontPackExceedsLimit
				}
				totalBytes += slot.dataLen
				slots = append(slots, slot)
				fontIdx++
			}
		}
		off = e.end
	}
	return slots, totalBytes, nil
}

func (r *rangeReader) inspectAttachedFile(attached mkvElem, index int) (mkvFontSlot, bool, error) {
	var slot mkvFontSlot
	slot.index = index
	for off, n := attached.start, 0; off < attached.end && n < 32; n++ {
		e, err := r.mkvFontElement(off, attached.end)
		if err != nil {
			return mkvFontSlot{}, false, err
		}
		if e.end <= off {
			return mkvFontSlot{}, false, errMKV
		}
		size := e.end - e.start
		switch e.id {
		case mkvFileNameID:
			if size > 0 && size <= 1024 {
				b, err := r.readFontBytes(e.start, int(size))
				if err != nil {
					return mkvFontSlot{}, false, err
				}
				slot.rawName = string(b)
			}
		case mkvFileMimeTypeID:
			if size > 0 && size <= 256 {
				b, err := r.readFontBytes(e.start, int(size))
				if err != nil {
					return mkvFontSlot{}, false, err
				}
				slot.mimeType = string(b)
			}
		case mkvFileDataID:
			slot.dataOff = e.start
			slot.dataLen = size
		}
		off = e.end
	}
	if slot.dataLen <= 0 || !IsFontAttachmentMeta(slot.rawName, slot.mimeType) {
		return mkvFontSlot{}, false, nil
	}
	return slot, true, nil
}

func (r *rangeReader) readMKVASSHeadersAndEvents(seg, tracks mkvElem) ([]byte, error) {
	return r.readMKVTrackASS(seg, tracks, -1)
}

func (r *rangeReader) readMKVTrackASS(seg, tracks mkvElem, streamIndex int) ([]byte, error) {
	if tracks.end-tracks.start <= 0 || tracks.end-tracks.start > 4<<20 {
		return nil, errMKV
	}
	tb, err := r.readFontBytes(tracks.start, int(tracks.end-tracks.start))
	if err != nil {
		return nil, err
	}
	entries, err := mkvChildren(tb)
	if err != nil {
		return nil, err
	}
	var matched []mkvASSTrack
	curIndex := 0
	for _, c := range entries {
		if c.id != mkvTrackEntryID {
			continue
		}
		fields, fieldErr := mkvChildren(c.b)
		if fieldErr != nil {
			return nil, fieldErr
		}
		var num, typ, defDur uint64
		var codec string
		var priv []byte
		for _, f := range fields {
			switch f.id {
			case mkvTrackNumberID:
				num = mkvUint(f.b)
			case mkvTrackTypeID:
				typ = mkvUint(f.b)
			case mkvCodecIDID:
				codec = string(f.b)
			case mkvCodecPrivateID:
				priv = f.b
			case mkvDefaultDurationID:
				defDur = mkvUint(f.b)
			}
		}
		// Match strictly by 0-based stream index (curIndex), never conflating
		// 1-based Matroska TrackNumber with streamIndex.
		if streamIndex < 0 || curIndex == streamIndex {
			if typ == 0x11 && IsASSCodec(codec) && num > 0 {
				matched = append(matched, mkvASSTrack{
					trackNum:    num,
					codecPriv:   priv,
					defaultDur:  defDur,
					streamIndex: curIndex,
				})
			}
			if streamIndex >= 0 {
				break
			}
		}
		curIndex++
	}
	if len(matched) == 0 {
		return nil, errMKV
	}

	trackMap := make(map[uint64]mkvASSTrack, len(matched))
	var out strings.Builder
	for _, tr := range matched {
		trackMap[tr.trackNum] = tr
		hdr := strings.TrimRight(string(tr.codecPriv), "\r\n\x00")
		if hdr != "" {
			out.WriteString(hdr)
			out.WriteByte('\n')
		}
	}
	if !strings.Contains(strings.ToLower(out.String()), "[events]") {
		if out.Len() == 0 {
			out.WriteString(ASSHeader)
		} else {
			out.WriteString("\n[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
		}
	}
	if out.Len() > MaxASSScriptBytes {
		return nil, fmt.Errorf("%w: header exceeds script limit", ErrASSTrackIncomplete)
	}

	scale := uint64(1000000)
	off := seg.start
	for count := 0; off < seg.end && count < maxMKVSegmentElements; count++ {
		e, elemErr := r.mkvFontElement(off, seg.end)
		if elemErr != nil {
			return nil, fmt.Errorf("%w: %w", ErrASSTrackIncomplete, elemErr)
		}
		if e.end <= off || e.end > seg.end {
			return nil, fmt.Errorf("%w: %w", ErrASSTrackIncomplete, errMKV)
		}
		switch e.id {
		case mkvInfoID:
			infoSize := e.end - e.start
			if infoSize < 0 || infoSize > 1<<20 {
				return nil, fmt.Errorf("%w: invalid SegmentInfo size", ErrASSTrackIncomplete)
			}
			if infoSize > 0 {
				ib, readErr := r.readFontBytes(e.start, int(infoSize))
				if readErr != nil {
					return nil, fmt.Errorf("%w: %w", ErrASSTrackIncomplete, readErr)
				}
				children, childErr := mkvChildren(ib)
				if childErr != nil {
					return nil, fmt.Errorf("%w: %w", ErrASSTrackIncomplete, childErr)
				}
				for _, c := range children {
					if c.id == mkvTimecodeScaleID {
						if v := mkvUint(c.b); v > 0 {
							scale = v
						}
					}
				}
			}
		case mkvClusterID:
			if err := r.appendClusterASSEventsFromElem(&out, e, scale, trackMap); err != nil {
				return nil, err
			}
		}
		off = e.end
	}
	if off < seg.end {
		return nil, fmt.Errorf("%w: segment element limit reached before end of segment", ErrASSTrackIncomplete)
	}
	if out.Len() > MaxASSScriptBytes {
		return nil, fmt.Errorf("%w: script exceeds %d bytes", ErrASSTrackIncomplete, MaxASSScriptBytes)
	}
	return []byte(out.String()), nil
}

func (r *rangeReader) appendClusterASSEventsFromElem(out *strings.Builder, cluster mkvElem, scale uint64, tracks map[uint64]mkvASSTrack) error {
	clusterSize := cluster.end - cluster.start
	if clusterSize < 0 {
		return fmt.Errorf("%w: negative cluster size", ErrASSTrackIncomplete)
	}
	if clusterSize == 0 {
		return nil
	}
	if clusterSize <= directClusterReadBytes {
		cb, err := r.readFontBytes(cluster.start, int(clusterSize))
		if err != nil {
			return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, err)
		}
		return appendClusterASSEvents(out, cb, scale, tracks)
	}
	// Large cluster (> 1 MiB, including > 4 MiB high-bitrate video clusters):
	// step through child elements via range reads so video payloads are skipped
	// without allocating whole-cluster buffers or dropping subtitle cues.
	var clusterTS uint64
	off := cluster.start
	for count := 0; off < cluster.end && count < maxMKVClusterElements; count++ {
		ce, err := r.mkvFontElement(off, cluster.end)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, err)
		}
		if ce.end <= off || ce.end > cluster.end {
			return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, errMKV)
		}
		elemSize := ce.end - ce.start
		switch ce.id {
		case mkvTimestampID:
			if elemSize < 0 || elemSize > 8 {
				return fmt.Errorf("%w: invalid cluster timestamp size", ErrASSTrackIncomplete)
			}
			if elemSize > 0 {
				tb, err := r.readFontBytes(ce.start, int(elemSize))
				if err != nil {
					return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, err)
				}
				clusterTS = mkvUint(tb)
			}
		case mkvSimpleBlockID:
			if err := r.readAndAppendMKVASSBlockElem(out, ce, clusterTS, 0, scale, tracks); err != nil {
				return err
			}
		case mkvBlockGroupID:
			if err := r.readAndAppendMKVASSBlockGroupElem(out, ce, clusterTS, scale, tracks); err != nil {
				return err
			}
		}
		off = ce.end
	}
	if off < cluster.end {
		return fmt.Errorf("%w: cluster element limit reached before end of cluster", ErrASSTrackIncomplete)
	}
	return nil
}

func (r *rangeReader) readAndAppendMKVASSBlockElem(out *strings.Builder, blockElem mkvElem, clusterTS, blockDur, scale uint64, tracks map[uint64]mkvASSTrack) error {
	blockSize := blockElem.end - blockElem.start
	if blockSize < 4 {
		return fmt.Errorf("%w: truncated Matroska block", ErrASSTrackIncomplete)
	}
	peekLen := int(min(blockSize, int64(12)))
	hdr, err := r.readFontBytes(blockElem.start, peekLen)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, err)
	}
	trackNum, n, err := mkvVint(hdr, false)
	if err != nil || int64(n+3) > blockSize {
		return fmt.Errorf("%w: invalid Matroska block header", ErrASSTrackIncomplete)
	}
	if _, wanted := tracks[trackNum]; !wanted {
		return nil
	}
	if blockSize > MaxASSScriptBytes {
		return fmt.Errorf("%w: subtitle block exceeds size limit", ErrASSTrackIncomplete)
	}
	var fullBlock []byte
	if int64(len(hdr)) == blockSize {
		fullBlock = hdr
	} else {
		fullBlock, err = r.readFontBytes(blockElem.start, int(blockSize))
		if err != nil {
			return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, err)
		}
	}
	return appendMKVASSBlock(out, fullBlock, clusterTS, blockDur, scale, tracks)
}

func (r *rangeReader) readAndAppendMKVASSBlockGroupElem(out *strings.Builder, groupElem mkvElem, clusterTS, scale uint64, tracks map[uint64]mkvASSTrack) error {
	var (
		blockElem mkvElem
		hasBlock  bool
		blockDur  uint64
	)
	off := groupElem.start
	for count := 0; off < groupElem.end && count < 64; count++ {
		ge, err := r.mkvFontElement(off, groupElem.end)
		if err != nil || ge.end <= off || ge.end > groupElem.end {
			return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, errMKV)
		}
		elemSize := ge.end - ge.start
		switch ge.id {
		case mkvBlockID:
			blockElem = ge
			hasBlock = true
		case mkvBlockDurationID:
			if elemSize < 0 || elemSize > 8 {
				return fmt.Errorf("%w: invalid block duration size", ErrASSTrackIncomplete)
			}
			if elemSize > 0 {
				db, err := r.readFontBytes(ge.start, int(elemSize))
				if err != nil {
					return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, err)
				}
				blockDur = mkvUint(db)
			}
		}
		off = ge.end
	}
	if off < groupElem.end {
		return fmt.Errorf("%w: truncated block group", ErrASSTrackIncomplete)
	}
	if !hasBlock {
		return nil
	}
	return r.readAndAppendMKVASSBlockElem(out, blockElem, clusterTS, blockDur, scale, tracks)
}

func appendClusterASSEvents(out *strings.Builder, clusterBytes []byte, scale uint64, tracks map[uint64]mkvASSTrack) error {
	children, err := mkvChildren(clusterBytes)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, err)
	}
	var clusterTS uint64
	for _, c := range children {
		if c.id == mkvTimestampID {
			if len(c.b) > 8 {
				return fmt.Errorf("%w: invalid cluster timestamp size", ErrASSTrackIncomplete)
			}
			clusterTS = mkvUint(c.b)
			break
		}
	}
	for _, c := range children {
		switch c.id {
		case mkvSimpleBlockID:
			if err := appendMKVASSBlock(out, c.b, clusterTS, 0, scale, tracks); err != nil {
				return err
			}
		case mkvBlockGroupID:
			groupChildren, err := mkvChildren(c.b)
			if err != nil {
				return fmt.Errorf("%w: %w", ErrASSTrackIncomplete, err)
			}
			var blockPayload []byte
			var blockDur uint64
			for _, gc := range groupChildren {
				switch gc.id {
				case mkvBlockID:
					blockPayload = gc.b
				case mkvBlockDurationID:
					if len(gc.b) > 8 {
						return fmt.Errorf("%w: invalid block duration size", ErrASSTrackIncomplete)
					}
					blockDur = mkvUint(gc.b)
				}
			}
			if len(blockPayload) > 0 {
				if err := appendMKVASSBlock(out, blockPayload, clusterTS, blockDur, scale, tracks); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func appendMKVASSBlock(out *strings.Builder, block []byte, clusterTS, blockDur, scale uint64, tracks map[uint64]mkvASSTrack) error {
	trackNum, n, err := mkvVint(block, false)
	if err != nil || len(block) < n+3 {
		return fmt.Errorf("%w: invalid Matroska block header", ErrASSTrackIncomplete)
	}
	tr, ok := tracks[trackNum]
	if !ok {
		return nil
	}
	relTS := int64(int16(binary.BigEndian.Uint16(block[n : n+2])))
	payload := strings.TrimRight(string(block[n+3:]), "\r\n\x00")
	if payload == "" {
		return nil
	}
	if hasASSRecordPrefix(strings.TrimSpace(payload), "Dialogue:") {
		out.WriteString(strings.TrimSpace(payload))
		out.WriteByte('\n')
		if out.Len() > MaxASSScriptBytes {
			return fmt.Errorf("%w: script exceeds %d bytes", ErrASSTrackIncomplete, MaxASSScriptBytes)
		}
		return nil
	}
	startTicks := int64(clusterTS) + relTS
	if startTicks < 0 {
		startTicks = 0
	}
	start := time.Duration(uint64(startTicks) * scale)
	var end time.Duration
	switch {
	case blockDur > 0:
		end = start + time.Duration(blockDur*scale)
	case tr.defaultDur > 0:
		end = start + time.Duration(tr.defaultDur)
	default:
		end = start + 2*time.Second
	}
	// Matroska S_TEXT/ASS block format: ReadOrder,Layer,Style,Name,MarginL,MarginR,MarginV,Effect,Text
	firstComma := strings.IndexByte(payload, ',')
	if firstComma >= 0 {
		rest := payload[firstComma+1:]
		secondComma := strings.IndexByte(rest, ',')
		if secondComma >= 0 {
			layer := strings.TrimSpace(rest[:secondComma])
			tail := rest[secondComma+1:]
			fmt.Fprintf(out, "Dialogue: %s,%s,%s,%s\n", layer, assTS(start), assTS(end), tail)
			if out.Len() > MaxASSScriptBytes {
				return fmt.Errorf("%w: script exceeds %d bytes", ErrASSTrackIncomplete, MaxASSScriptBytes)
			}
			return nil
		}
	}
	fmt.Fprintf(out, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assTS(start), assTS(end), payload)
	if out.Len() > MaxASSScriptBytes {
		return fmt.Errorf("%w: script exceeds %d bytes", ErrASSTrackIncomplete, MaxASSScriptBytes)
	}
	return nil
}
